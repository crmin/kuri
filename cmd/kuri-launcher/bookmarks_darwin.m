//go:build cgo

#import "bookmarks_darwin.h"
#import "settings_darwin.h"

extern void kuriGoBookmarkRequest(char *request, uint64_t sequence);
NSNotificationName const KuriBookmarksDidChange = @"KuriBookmarksDidChange";

// Both callers enqueue on the AppKit thread. Only the CLI runs in the background.
static NSMutableDictionary *bookmarkReplies;
static uint64_t bookmarkSequence;

void kuriRequestBookmarks(NSDictionary *request, void (^reply)(NSDictionary *response)) {
    NSData *data = [NSJSONSerialization dataWithJSONObject:request options:0 error:NULL];
    if (!data) {
        reply(@{@"error": @"Could not prepare the bookmark request."});
        return;
    }
    NSString *json = [[[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding] autorelease];
    if (!bookmarkReplies) bookmarkReplies = [[NSMutableDictionary alloc] init];
    uint64_t sequence = ++bookmarkSequence;
    bookmarkReplies[@(sequence)] = [[reply copy] autorelease];
    kuriGoBookmarkRequest((char *)json.UTF8String, sequence);
}

void kuriRenderBookmarks(const char *json, uint64_t sequence) {
    @autoreleasepool {
        NSData *data = [NSData dataWithBytes:json length:strlen(json)];
        id value = [NSJSONSerialization JSONObjectWithData:data options:0 error:NULL];
        NSDictionary *response = [value isKindOfClass:NSDictionary.class] ? value :
            @{@"error": @"Could not read the bookmark response."};
        void (^complete)(void) = ^{
            void (^reply)(NSDictionary *) = [[bookmarkReplies[@(sequence)] copy] autorelease];
            [bookmarkReplies removeObjectForKey:@(sequence)];
            if (reply) reply(response);
        };
        if (NSThread.isMainThread) complete();
        else dispatch_async(dispatch_get_main_queue(), complete);
    }
}

@interface KuriSettings (BookmarkDialogs)
- (NSStackView *)createDialog:(NSString *)title size:(NSSize)size;
- (NSStackView *)dialogActions:(NSString *)title action:(SEL)action in:(NSStackView *)stack;
- (void)showDialog;
- (void)closeDialog:(id)sender;
- (void)submitBookmark:(id)sender;
@end

static NSPasteboardType const bookmarkDragType = @"com.github.crmin.kuri.bookmark";
static const CGFloat bookmarkRowHeight = 22;
static NSArray<NSString *> *bookmarkFields(void) {
    return @[@"context", @"namespace", @"service", @"port_name"];
}

static NSString *bookmarkString(id value) {
    return [value isKindOfClass:NSString.class] ? value : @"";
}

static NSArray *bookmarkArray(id value) {
    return [value isKindOfClass:NSArray.class] ? value : @[];
}

static NSStackView *bookmarkStack(NSUserInterfaceLayoutOrientation orientation, CGFloat spacing) {
    NSStackView *stack = [NSStackView stackViewWithViews:@[]];
    stack.orientation = orientation;
    stack.alignment = orientation == NSUserInterfaceLayoutOrientationVertical ?
        NSLayoutAttributeLeading : NSLayoutAttributeCenterY;
    stack.spacing = spacing;
    return stack;
}

static void bookmarkAddFullWidth(NSStackView *stack, NSView *view) {
    [stack addArrangedSubview:view];
    [view.widthAnchor constraintEqualToAnchor:stack.widthAnchor].active = YES;
}

static NSTextField *bookmarkDetail(NSString *message) {
    NSTextField *detail = [NSTextField wrappingLabelWithString:message];
    detail.textColor = NSColor.secondaryLabelColor;
    [detail setContentCompressionResistancePriority:NSLayoutPriorityDefaultLow forOrientation:NSLayoutConstraintOrientationHorizontal];
    return detail;
}

static NSScrollView *bookmarkTableScroll(NSTableView *table) {
    NSScrollView *scroll = [[[NSScrollView alloc] initWithFrame:NSZeroRect] autorelease];
    scroll.hasVerticalScroller = YES;
    scroll.autohidesScrollers = YES;
    scroll.borderType = NSBezelBorder;
    scroll.documentView = table;
    table.autoresizingMask = NSViewWidthSizable;
    return scroll;
}

@interface KuriBookmarkSettings ()
- (void)choose:(NSString *)choice forField:(NSInteger)field;
@end

@interface KuriBookmarkChoices : NSObject <NSTableViewDataSource, NSTableViewDelegate, NSSearchFieldDelegate, NSPopoverDelegate>
@property(nonatomic, assign) KuriBookmarkSettings *owner;
@property(nonatomic, retain) NSPopover *popover;
@property(nonatomic, retain) NSSearchField *search;
@property(nonatomic, retain) NSTableView *table;
@property(nonatomic, copy) NSArray<NSString *> *options;
@property(nonatomic, copy) NSArray<NSString *> *filtered;
@property(nonatomic) NSInteger field;
- (void)showBelow:(NSButton *)button options:(NSArray<NSString *> *)options;
- (void)close;
@end

@implementation KuriBookmarkChoices
- (instancetype)init {
    self = [super init];
    if (!self) return nil;
    self.popover = [[[NSPopover alloc] init] autorelease];
    self.popover.behavior = NSPopoverBehaviorTransient;
    self.popover.animates = NO;
    self.popover.delegate = self;
    NSViewController *content = [[[NSViewController alloc] init] autorelease];
    NSStackView *body = bookmarkStack(NSUserInterfaceLayoutOrientationVertical, 6);
    body.edgeInsets = NSEdgeInsetsMake(8, 8, 8, 8);
    self.search = [[[NSSearchField alloc] initWithFrame:NSZeroRect] autorelease];
    self.search.placeholderString = @"Search...";
    self.search.delegate = self;
    [self.search setAccessibilityLabel:@"Search options"];
    [body addArrangedSubview:self.search];

    self.table = [[[NSTableView alloc] initWithFrame:NSZeroRect] autorelease];
    self.table.headerView = nil;
    self.table.style = NSTableViewStylePlain;
    self.table.rowHeight = bookmarkRowHeight;
    [self.table addTableColumn:[[[NSTableColumn alloc] initWithIdentifier:@"option"] autorelease]];
    self.table.dataSource = self;
    self.table.delegate = self;
    self.table.target = self;
    self.table.action = @selector(selectClicked:);
    NSScrollView *scroll = bookmarkTableScroll(self.table);
    [body addArrangedSubview:scroll];
    [scroll.heightAnchor constraintEqualToConstant:bookmarkRowHeight * 7].active = YES;
    [scroll.widthAnchor constraintEqualToConstant:244].active = YES;
    [self.search.widthAnchor constraintEqualToAnchor:scroll.widthAnchor].active = YES;
    content.view = body;
    self.popover.contentViewController = content;
    self.popover.contentSize = NSMakeSize(260, 200);
    return self;
}

- (void)showBelow:(NSButton *)button options:(NSArray<NSString *> *)options {
    [self close];
    self.field = button.tag;
    self.options = options;
    self.search.stringValue = @"";
    [self filter:@""];
    [self.popover showRelativeToRect:button.bounds ofView:button preferredEdge:NSMaxYEdge];
}

- (void)popoverDidShow:(NSNotification *)note { [self.search.window makeFirstResponder:self.search]; }
- (void)close { [self.popover close]; }

- (NSString *)labelForChoice:(NSString *)choice {
    return choice.length ? choice : @"Default port";
}

- (void)filter:(NSString *)query {
    NSMutableArray *visible = [NSMutableArray array];
    for (NSString *option in self.options) {
        if (!query.length || [[self labelForChoice:option] localizedCaseInsensitiveContainsString:query])
            [visible addObject:option];
    }
    self.filtered = visible;
    [self.table reloadData];
    if (self.filtered.count) {
        [self.table selectRowIndexes:[NSIndexSet indexSetWithIndex:0] byExtendingSelection:NO];
        [self.table scrollRowToVisible:0];
    } else {
        [self.table deselectAll:nil];
    }
}

- (NSInteger)numberOfRowsInTableView:(NSTableView *)tableView { return self.filtered.count; }

- (NSView *)tableView:(NSTableView *)tableView viewForTableColumn:(NSTableColumn *)column row:(NSInteger)row {
    NSTextField *label = [NSTextField labelWithString:[self labelForChoice:self.filtered[row]]];
    label.lineBreakMode = NSLineBreakByTruncatingTail;
    label.toolTip = label.stringValue;
    return label;
}

- (void)controlTextDidChange:(NSNotification *)notification {
    [self filter:self.search.stringValue];
}

- (void)selectRow:(NSInteger)row {
    if (row < 0 || row >= (NSInteger)self.filtered.count) return;
    NSString *choice = [[self.filtered[row] retain] autorelease];
    [self close];
    [self.owner choose:choice forField:self.field];
}

- (void)selectClicked:(NSTableView *)sender { [self selectRow:sender.clickedRow]; }

- (BOOL)control:(NSControl *)control textView:(NSTextView *)textView doCommandBySelector:(SEL)command {
    if (textView.hasMarkedText) return NO;
    if (command == @selector(cancelOperation:)) {
        [self close];
    } else if (command == @selector(insertNewline:)) {
        [self selectRow:self.table.selectedRow];
    } else if (command == @selector(moveDown:) || command == @selector(moveUp:)) {
        if (self.filtered.count) {
            NSInteger step = command == @selector(moveDown:) ? 1 : -1;
            NSInteger row = MAX(0, MIN((NSInteger)self.filtered.count - 1, self.table.selectedRow + step));
            [self.table selectRowIndexes:[NSIndexSet indexSetWithIndex:row] byExtendingSelection:NO];
            [self.table scrollRowToVisible:row];
        }
    } else {
        return NO;
    }
    return YES;
}

- (void)dealloc {
    self.popover.delegate = nil;
    self.table.delegate = nil;
    self.table.dataSource = nil;
    self.search.delegate = nil;
    [_popover release];
    [_options release];
    [_filtered release];
    [_search release];
    [_table release];
    [super dealloc];
}
@end

@interface KuriBookmarkSettings () <NSTableViewDataSource, NSTableViewDelegate>
@property(nonatomic, assign) KuriSettings *settings;
@property(nonatomic, retain) NSView *view;
@property(nonatomic, retain) NSTableView *table;
@property(nonatomic, copy) NSArray<NSDictionary *> *items;
@property(nonatomic, retain) NSButton *add;
@property(nonatomic, retain) NSButton *remove;
@property(nonatomic, retain) NSTextField *status;
@property(nonatomic, copy) NSArray<NSString *> *dragOrder;
@property(nonatomic) BOOL available;
@property(nonatomic) BOOL loaded;
@property(nonatomic) BOOL working;
@property(nonatomic) NSUInteger listRequest;

@property(nonatomic, retain) NSTextField *name;
@property(nonatomic, retain) NSTextField *editorStatus;
@property(nonatomic, retain) NSButton *confirm;
@property(nonatomic, retain) NSButton *cancel;
@property(nonatomic, copy) NSArray<NSButton *> *selects;
@property(nonatomic, retain) KuriBookmarkChoices *choices;
@property(nonatomic, retain) NSMutableDictionary<NSString *, NSString *> *selection;
@property(nonatomic, retain) NSMutableDictionary<NSString *, NSArray *> *options;
@property(nonatomic, copy) NSDictionary *kubeconfig;
@property(nonatomic) NSUInteger editorRequest;
@property(nonatomic) BOOL loadingOptions;
@property(nonatomic) BOOL saving;
@end

@implementation KuriBookmarkSettings
- (instancetype)initWithSettings:(KuriSettings *)settings {
    self = [super init];
    if (!self) return nil;
    self.settings = settings;
    self.items = @[];

    NSStackView *stack = bookmarkStack(NSUserInterfaceLayoutOrientationVertical, 10);
    NSTextField *title = [NSTextField labelWithString:@"Bookmark"];
    title.font = [NSFont boldSystemFontOfSize:13];
    [stack addArrangedSubview:title];
    bookmarkAddFullWidth(stack, bookmarkDetail(@"Open services from the menu bar, even when no port forward is active."));

    self.table = [[[NSTableView alloc] initWithFrame:NSZeroRect] autorelease];
    self.table.identifier = @"bookmark-list";
    self.table.style = NSTableViewStylePlain;
    self.table.columnAutoresizingStyle = NSTableViewUniformColumnAutoresizingStyle;
    self.table.allowsColumnReordering = NO;
    self.table.allowsMultipleSelection = NO;
    self.table.usesAlternatingRowBackgroundColors = YES;
    self.table.rowHeight = bookmarkRowHeight;
    self.table.intercellSpacing = NSMakeSize(4, 0);
    [self.table setAccessibilityLabel:@"Bookmarks"];
    NSArray *keys = @[@"name", @"context", @"namespace", @"service", @"port_name"];
    NSArray *titles = @[@"Name", @"Context", @"Namespace", @"Service", @"Port Name"];
    NSArray *widths = @[@130, @120, @100, @120, @100];
    for (NSUInteger index = 0; index < keys.count; index++) {
        NSTableColumn *column = [[[NSTableColumn alloc] initWithIdentifier:keys[index]] autorelease];
        column.title = titles[index];
        column.width = [widths[index] doubleValue];
        column.minWidth = 69;
        column.resizingMask = NSTableColumnAutoresizingMask | NSTableColumnUserResizingMask;
        [self.table addTableColumn:column];
    }
    self.table.dataSource = self;
    self.table.delegate = self;
    [self.table registerForDraggedTypes:@[bookmarkDragType]];
    [self.table setDraggingSourceOperationMask:NSDragOperationMove forLocal:YES];

    NSScrollView *scroll = bookmarkTableScroll(self.table);
    bookmarkAddFullWidth(stack, scroll);
    [scroll.heightAnchor constraintEqualToConstant:bookmarkRowHeight * 8 + NSHeight(self.table.headerView.frame) + 2].active = YES;
    NSStackView *buttons = bookmarkStack(NSUserInterfaceLayoutOrientationHorizontal, 4);
    self.add = [NSButton buttonWithTitle:@"+" target:self action:@selector(openEditor:)];
    self.remove = [NSButton buttonWithTitle:@"-" target:self action:@selector(removeItem:)];
    [self.add setAccessibilityLabel:@"Add bookmark"];
    [self.remove setAccessibilityLabel:@"Remove bookmark"];
    [buttons addArrangedSubview:self.add];
    [buttons addArrangedSubview:self.remove];
    [stack addArrangedSubview:buttons];
    self.status = bookmarkDetail(@"");
    self.status.hidden = YES;
    bookmarkAddFullWidth(stack, self.status);
    self.view = stack;
    [self refreshControls];
    return self;
}

- (void)setAvailable:(BOOL)available {
    _available = available;
    [self refreshControls];
}

- (void)refreshControls {
    BOOL editable = self.available && self.loaded && !self.working;
    self.add.enabled = editable;
    self.remove.enabled = editable && self.table.selectedRow >= 0;
}

- (void)showStatus:(NSString *)message {
    self.status.stringValue = message ?: @"";
    self.status.hidden = !message.length;
}

- (void)reload {
    if (self.working || self.dragOrder || self.selects) return;
    NSUInteger request = ++self.listRequest;
    kuriRequestBookmarks(@{@"operation": @"list"}, ^(NSDictionary *response) {
        if (request != self.listRequest) return;
        NSString *error = response[@"error"];
        self.loaded = !error.length;
        if (self.loaded) [self showItems:bookmarkArray(response[@"items"])];
        [self showStatus:error];
        [self refreshControls];
    });
}

- (void)showItems:(NSArray *)items {
    NSInteger selected = self.table.selectedRow;
    NSString *idToSelect = selected >= 0 && selected < (NSInteger)self.items.count ? self.items[selected][@"id"] : nil;
    self.items = items;
    [self.table reloadData];
    NSUInteger index = [self.items indexOfObjectPassingTest:^BOOL(NSDictionary *item, NSUInteger position, BOOL *stop) {
        return [item[@"id"] isEqualToString:idToSelect];
    }];
    if (index != NSNotFound) [self.table selectRowIndexes:[NSIndexSet indexSetWithIndex:index] byExtendingSelection:NO];
    else [self.table deselectAll:nil];
}

- (NSInteger)numberOfRowsInTableView:(NSTableView *)tableView { return self.items.count; }

- (NSView *)tableView:(NSTableView *)tableView viewForTableColumn:(NSTableColumn *)column row:(NSInteger)row {
    NSTextField *field = [tableView makeViewWithIdentifier:column.identifier owner:self];
    if (!field) {
        field = [NSTextField labelWithString:@""];
        field.identifier = column.identifier;
        field.lineBreakMode = NSLineBreakByTruncatingTail;
    }
    field.stringValue = bookmarkString(self.items[row][column.identifier]);
    field.toolTip = field.stringValue;
    return field;
}

- (void)tableViewSelectionDidChange:(NSNotification *)note { [self refreshControls]; }

- (id<NSPasteboardWriting>)tableView:(NSTableView *)tableView pasteboardWriterForRow:(NSInteger)row {
    if (!self.add.enabled || row < 0 || row >= (NSInteger)self.items.count) return nil;
    NSPasteboardItem *item = [[[NSPasteboardItem alloc] init] autorelease];
    [item setString:self.items[row][@"id"] forType:bookmarkDragType];
    return item;
}

- (void)tableView:(NSTableView *)tableView draggingSession:(NSDraggingSession *)session
    willBeginAtPoint:(NSPoint)screenPoint forRowIndexes:(NSIndexSet *)rowIndexes {
    self.dragOrder = [self.items valueForKey:@"id"];
    ++self.listRequest;
}

- (NSDragOperation)tableView:(NSTableView *)tableView validateDrop:(id<NSDraggingInfo>)info
    proposedRow:(NSInteger)row proposedDropOperation:(NSTableViewDropOperation)operation {
    if ([info draggingSource] != tableView || !self.dragOrder || self.working) return NSDragOperationNone;
    [tableView setDropRow:row dropOperation:NSTableViewDropAbove];
    return NSDragOperationMove;
}

- (BOOL)tableView:(NSTableView *)tableView acceptDrop:(id<NSDraggingInfo>)info
    row:(NSInteger)row dropOperation:(NSTableViewDropOperation)operation {
    if ([info draggingSource] != tableView || operation != NSTableViewDropAbove ||
        row < 0 || row > (NSInteger)self.dragOrder.count || self.working) return NO;
    NSString *item = [[info draggingPasteboard] stringForType:bookmarkDragType];
    if (!item) return NO;
    NSUInteger from = [self.dragOrder indexOfObject:item];
    if (from == NSNotFound) return NO;
    NSUInteger to = row > (NSInteger)from ? (NSUInteger)row - 1 : (NSUInteger)row;
    if (from == to) return NO;
    NSMutableArray *order = [[self.dragOrder mutableCopy] autorelease];
    [order removeObjectAtIndex:from];
    [order insertObject:item atIndex:to];
    [self mutate:@{@"operation": @"reorder", @"before": self.dragOrder, @"order": order}];
    return YES;
}

- (void)tableView:(NSTableView *)tableView draggingSession:(NSDraggingSession *)session
    endedAtPoint:(NSPoint)screenPoint operation:(NSDragOperation)operation {
    self.dragOrder = nil;
}

- (void)removeItem:(NSButton *)sender {
    NSInteger row = self.table.selectedRow;
    if (!self.remove.enabled || row < 0 || row >= (NSInteger)self.items.count) return;
    [self mutate:@{@"operation": @"remove", @"id": self.items[row][@"id"]}];
}

- (void)mutate:(NSDictionary *)request {
    self.working = YES;
    ++self.listRequest;
    [self refreshControls];
    kuriRequestBookmarks(request, ^(NSDictionary *response) {
        self.working = NO;
        if ([response[@"items"] isKindOfClass:NSArray.class]) [self showItems:response[@"items"]];
        [self showStatus:response[@"error"]];
        [self refreshControls];
        [self notifyChange:response];
    });
}

- (void)notifyChange:(NSDictionary *)response {
    if ([response[@"changed"] boolValue]) {
        [[NSNotificationCenter defaultCenter] postNotificationName:KuriBookmarksDidChange object:nil];
    }
}

- (void)openEditor:(NSButton *)sender {
    if (!self.add.enabled) return;
    NSStackView *body = [self.settings createDialog:@"Add Bookmark Item" size:NSMakeSize(480, 285)];
    self.name = [[[NSTextField alloc] initWithFrame:NSZeroRect] autorelease];
    self.name.identifier = @"bookmark-name";
    [self.name setAccessibilityLabel:@"Name"];
    [self addEditorRow:@"Name" optional:YES control:self.name to:body];
    NSMutableArray *selects = [NSMutableArray array];
    NSArray *titles = @[@"Context", @"Namespace", @"Service", @"Port Name"];
    for (NSInteger index = 0; index < (NSInteger)titles.count; index++) {
        NSButton *select = [NSButton buttonWithTitle:@"Choose..." target:self action:@selector(openChoices:)];
        select.alignment = NSTextAlignmentLeft;
        select.image = [NSImage imageWithSystemSymbolName:@"chevron.up.chevron.down" accessibilityDescription:nil];
        select.imagePosition = NSImageTrailing;
        select.bezelStyle = NSBezelStyleRounded;
        select.lineBreakMode = NSLineBreakByTruncatingTail;
        select.tag = index;
        select.identifier = bookmarkFields()[index];
        [select setAccessibilityLabel:titles[index]];
        [self addEditorRow:titles[index] optional:index == 3 control:select to:body];
        [selects addObject:select];
    }
    self.selects = selects;
    self.editorStatus = bookmarkDetail(@"");
    self.editorStatus.hidden = YES;
    bookmarkAddFullWidth(body, self.editorStatus);
    NSStackView *actions = [self.settings dialogActions:@"Add item" action:@selector(submitBookmark:) in:body];
    self.cancel = (NSButton *)actions.arrangedSubviews[1];
    self.confirm = (NSButton *)actions.arrangedSubviews.lastObject;
    self.selection = [NSMutableDictionary dictionary];
    self.options = [NSMutableDictionary dictionary];
    self.choices = [[[KuriBookmarkChoices alloc] init] autorelease];
    self.choices.owner = self;
    [self.settings showDialog];
    [self loadField:0];
}

- (void)addEditorRow:(NSString *)title optional:(BOOL)optional control:(NSControl *)control to:(NSStackView *)body {
    NSStackView *row = bookmarkStack(NSUserInterfaceLayoutOrientationHorizontal, 12);
    NSTextField *label = [NSTextField labelWithString:title];
    if (optional) {
        NSMutableAttributedString *text = [[[NSMutableAttributedString alloc] initWithString:title] autorelease];
        [text appendAttributedString:[[[NSAttributedString alloc] initWithString:@" (optional)"
            attributes:@{NSForegroundColorAttributeName: NSColor.secondaryLabelColor}] autorelease]];
        label.attributedStringValue = text;
    }
    [row addArrangedSubview:label];
    [label.widthAnchor constraintEqualToConstant:142].active = YES;
    [row addArrangedSubview:control];
    [control setContentCompressionResistancePriority:NSLayoutPriorityDefaultLow forOrientation:NSLayoutConstraintOrientationHorizontal];
    bookmarkAddFullWidth(body, row);
}

- (void)openChoices:(NSButton *)button {
    if (!button.enabled) return;
    [self.choices showBelow:button options:self.options[bookmarkFields()[button.tag]]];
}

- (void)choose:(NSString *)choice forField:(NSInteger)field {
    if (!self.selects || self.saving) return;
    NSArray *keys = bookmarkFields();
    self.selection[keys[field]] = choice;
    for (NSInteger child = field + 1; child < (NSInteger)keys.count; child++) {
        [self.selection removeObjectForKey:keys[child]];
        [self.options removeObjectForKey:keys[child]];
    }
    [self showEditorStatus:nil error:NO];
    if (field + 1 < (NSInteger)keys.count) [self loadField:field + 1];
    else [self refreshEditor];
}

- (void)loadField:(NSInteger)field {
    NSArray *operations = @[@"contexts", @"namespaces", @"services", @"ports"];
    NSMutableDictionary *request = [NSMutableDictionary dictionaryWithDictionary:self.kubeconfig ?: @{}];
    [request addEntriesFromDictionary:self.selection];
    request[@"operation"] = operations[field];
    NSUInteger generation = ++self.editorRequest;
    self.loadingOptions = YES;
    if (!self.editorStatus.stringValue.length) [self showEditorStatus:@"Loading options..." error:NO];
    [self refreshEditor];
    kuriRequestBookmarks(request, ^(NSDictionary *response) {
        if (generation != self.editorRequest || !self.selects) return;
        self.loadingOptions = NO;
        if ([response[@"refresh"] boolValue]) {
            [self reloadEditor:response[@"error"]];
            return;
        }
        NSString *key = bookmarkFields()[field];
        NSArray *options = bookmarkArray(response[@"options"]);
        if (field == 3 && !response[@"error"]) options = [@[@""] arrayByAddingObjectsFromArray:options];
        self.options[key] = options;
        if (field == 0 && !response[@"error"]) {
            self.kubeconfig = @{@"kubeconfig_path": bookmarkString(response[@"kubeconfig_path"]),
                @"kubeconfig_fingerprint": bookmarkString(response[@"kubeconfig_fingerprint"])};
        }
        NSString *notice = response[@"error"];
        if (!notice.length && !options.count) notice = @"No options available.";
        if (!notice.length && field == 0 && [self.editorStatus.textColor isEqual:NSColor.systemRedColor])
            notice = self.editorStatus.stringValue;
        [self showEditorStatus:notice error:response[@"error"] != nil];
        [self refreshEditor];
    });
}

- (void)reloadEditor:(NSString *)message {
    [self.choices close];
    [self.selection removeAllObjects];
    [self.options removeAllObjects];
    self.kubeconfig = nil;
    [self showEditorStatus:message error:YES];
    [self loadField:0];
}

- (void)showEditorStatus:(NSString *)message error:(BOOL)error {
    self.editorStatus.stringValue = message ?: @"";
    self.editorStatus.textColor = error ? NSColor.systemRedColor : NSColor.secondaryLabelColor;
    self.editorStatus.hidden = !message.length;
}

- (void)refreshEditor {
    NSArray *fields = bookmarkFields();
    for (NSUInteger index = 0; index < self.selects.count; index++) {
        NSString *key = fields[index], *value = self.selection[key];
        NSButton *select = self.selects[index];
        BOOL parentReady = index == 0 || [self.selection[fields[index - 1]] length] > 0;
        select.title = value.length ? value : (index == 3 ? @"Default port" : @"Choose...");
        select.toolTip = value;
        select.enabled = !self.saving && parentReady && [self.options[key] count] > 0;
    }
    self.name.enabled = !self.saving;
    self.cancel.enabled = !self.saving;
    self.confirm.enabled = !self.saving && !self.loadingOptions &&
        [self.selection[@"context"] length] && [self.selection[@"namespace"] length] && [self.selection[@"service"] length];
}

- (void)addItem {
    if (!self.confirm.enabled || self.saving) return;
    [self.choices close];
    NSTextView *editor = (NSTextView *)self.name.currentEditor;
    NSMutableDictionary *request = [NSMutableDictionary dictionaryWithDictionary:self.kubeconfig ?: @{}];
    [request addEntriesFromDictionary:self.selection];
    request[@"operation"] = @"add";
    request[@"name"] = editor ? editor.string : self.name.stringValue;
    self.saving = YES;
    ++self.listRequest;
    [self refreshEditor];
    kuriRequestBookmarks(request, ^(NSDictionary *response) {
        self.saving = NO;
        [self refreshEditor];
        if ([response[@"duplicate"] boolValue]) return;
        if ([response[@"changed"] boolValue]) {
            [self showItems:bookmarkArray(response[@"items"])];
            if (self.items.count) [self.table scrollRowToVisible:self.items.count - 1];
            [self showStatus:nil];
            [self.settings closeDialog:nil];
            [self notifyChange:response];
        } else if ([response[@"refresh"] boolValue]) {
            [self reloadEditor:response[@"error"]];
        } else {
            [self showEditorStatus:response[@"error"] ?: @"Could not add the bookmark." error:YES];
        }
    });
}

- (void)endEditing {
    ++self.editorRequest;
    self.loadingOptions = NO;
    [self.choices close];
    self.choices = nil;
    self.name = nil;
    self.selects = nil;
    self.confirm = nil;
    self.cancel = nil;
    self.editorStatus = nil;
    self.selection = nil;
    self.options = nil;
    self.kubeconfig = nil;
}

- (void)dealloc {
    [self endEditing];
    self.table.delegate = nil;
    self.table.dataSource = nil;
    [_view release];
    [_table release];
    [_items release];
    [_add release];
    [_remove release];
    [_status release];
    [_dragOrder release];
    [super dealloc];
}
@end
