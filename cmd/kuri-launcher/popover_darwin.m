//go:build cgo

#import "popover_darwin.h"
#import "bookmarks_darwin.h"
#import "settings_darwin.h"
#import "shortcuts_darwin.h"

extern void kuriGoVisible(int visible);
extern void kuriGoAction(int action);

static const CGFloat recentCornerRadius = 12;

@interface KuriPopoverCanvas : NSView
@end

@implementation KuriPopoverCanvas
- (BOOL)isFlipped { return YES; }
@end

@interface KuriRecentMenuCanvas : KuriPopoverCanvas
@end

@implementation KuriRecentMenuCanvas
- (void)viewDidMoveToWindow {
    [super viewDidMoveToWindow];
    NSNotificationCenter *center = NSWorkspace.sharedWorkspace.notificationCenter;
    [center removeObserver:self name:NSWorkspaceAccessibilityDisplayOptionsDidChangeNotification object:nil];
    if (self.window) {
        [center addObserver:self selector:@selector(displayOptionsChanged:)
                       name:NSWorkspaceAccessibilityDisplayOptionsDidChangeNotification object:nil];
    }
}
- (void)displayOptionsChanged:(NSNotification *)notification { self.needsDisplay = YES; }
- (void)drawRect:(NSRect)dirtyRect {
    if (!NSWorkspace.sharedWorkspace.accessibilityDisplayShouldReduceTransparency) return;
    [[NSColor.windowBackgroundColor colorWithAlphaComponent:1] setFill];
    [[NSBezierPath bezierPathWithRoundedRect:self.bounds xRadius:recentCornerRadius yRadius:recentCornerRadius] fill];
}
@end

@interface KuriShortcutLabel : NSTextField
@end

@implementation KuriShortcutLabel
- (NSView *)hitTest:(NSPoint)point { return nil; }
@end

@interface KuriPopoverButton : NSButton
@property(nonatomic) BOOL tracksHover;
@property(nonatomic) BOOL hovered;
@property(nonatomic) BOOL submenuOpen;
@property(nonatomic, retain) NSTrackingArea *rowTrackingArea;
@property(nonatomic) SEL hoverAction;
@property(nonatomic) SEL leaveAction;
@property(nonatomic, copy) NSString *secondaryTitle;
- (void)setRowTitle:(NSString *)title;
@end

@implementation KuriPopoverButton
- (BOOL)showsHighlight { return self.tracksHover && (self.hovered || self.submenuOpen) && self.enabled; }

- (void)setRowTitle:(NSString *)title {
    NSColor *color = [self showsHighlight] ? NSColor.selectedMenuItemTextColor : NSColor.labelColor;
    if (!self.enabled) color = NSColor.disabledControlTextColor;
    NSMutableParagraphStyle *paragraph = [[[NSMutableParagraphStyle alloc] init] autorelease];
    paragraph.alignment = NSTextAlignmentLeft;
    paragraph.lineBreakMode = NSLineBreakByTruncatingTail;
    paragraph.firstLineHeadIndent = 6;
    paragraph.tailIndent = -6;
    NSMutableAttributedString *text = [[[NSMutableAttributedString alloc] initWithString:title attributes:@{
        NSForegroundColorAttributeName: color, NSFontAttributeName: self.font,
        NSParagraphStyleAttributeName: paragraph,
    }] autorelease];
    if (self.enabled && self.secondaryTitle.length && [title hasSuffix:self.secondaryTitle]) {
        NSColor *secondary = [self showsHighlight] ? [color colorWithAlphaComponent:0.78] : NSColor.secondaryLabelColor;
        NSRange suffix = NSMakeRange(title.length - self.secondaryTitle.length, self.secondaryTitle.length);
        [text addAttribute:NSForegroundColorAttributeName value:secondary range:suffix];
    }
    self.attributedTitle = text;
    self.needsDisplay = YES;
}
- (void)setEnabled:(BOOL)enabled {
    if (self.enabled == enabled) return;
    [super setEnabled:enabled];
    if (self.font) [self setRowTitle:self.title];
}
- (void)drawRect:(NSRect)dirtyRect {
    if ([self showsHighlight]) {
        [NSColor.selectedContentBackgroundColor setFill];
        [[NSBezierPath bezierPathWithRoundedRect:NSInsetRect(self.bounds, 0, 1) xRadius:4 yRadius:4] fill];
    }
    [super drawRect:dirtyRect];
}
- (void)updateTrackingAreas {
    [super updateTrackingAreas];
    if (self.tracksHover && !self.rowTrackingArea) {
        self.rowTrackingArea = [[[NSTrackingArea alloc] initWithRect:NSZeroRect
            options:NSTrackingMouseEnteredAndExited | NSTrackingActiveAlways | NSTrackingInVisibleRect
            owner:self userInfo:nil] autorelease];
        [self addTrackingArea:self.rowTrackingArea];
    }
}
- (void)mouseEntered:(NSEvent *)event {
    self.hovered = YES;
    [self setRowTitle:self.title];
    if (self.hoverAction && self.enabled) [NSApp sendAction:self.hoverAction to:self.target from:self];
}
- (void)mouseExited:(NSEvent *)event {
    self.hovered = NO;
    [self setRowTitle:self.title];
    if (self.leaveAction) [NSApp sendAction:self.leaveAction to:self.target from:self];
}
- (BOOL)acceptsFirstResponder { return self.enabled; }
- (void)viewDidChangeEffectiveAppearance {
    [super viewDidChangeEffectiveAppearance];
    if (self.font) [self setRowTitle:self.title];
}
- (void)dealloc {
    [_rowTrackingArea release];
    [_secondaryTitle release];
    [super dealloc];
}
@end

@interface KuriRecentMenuButton : KuriPopoverButton <NSAccessibilityButton>
@end

@implementation KuriRecentMenuButton
- (BOOL)isAccessibilityElement { return YES; }
- (NSAccessibilityRole)accessibilityRole { return NSAccessibilityMenuButtonRole; }
- (NSString *)accessibilityLabel { return self.title; }
- (BOOL)isAccessibilityExpanded { return self.submenuOpen; }
- (BOOL)accessibilityPerformPress {
    if (!self.enabled) return NO;
    [self performClick:nil];
    return YES;
}
@end

@class KuriBookmarkMenuView;

@interface KuriPopover ()
@property(nonatomic, retain) NSPopover *popover;
@property(nonatomic, retain) NSTextField *serverStatus;
@property(nonatomic, retain) NSSwitch *serverSwitch;
@property(nonatomic, retain) NSTextField *uptime;
@property(nonatomic, retain) NSTextField *count;
@property(nonatomic, retain) NSTextField *problem;
@property(nonatomic, retain) KuriPopoverButton *settingsButton;
@property(nonatomic, retain) NSTextField *settingsShortcut;
@property(nonatomic, retain) KuriPopoverButton *recentButton;
@property(nonatomic, retain) NSTextField *recentArrow;
@property(nonatomic, retain) KuriPopoverButton *quitButton;
@property(nonatomic, retain) NSTextField *shortcut;
@property(nonatomic, retain) NSBox *headerLine;
@property(nonatomic, retain) NSBox *rowsLine;
@property(nonatomic, retain) NSBox *quitLine;
@property(nonatomic, retain) NSBox *quitSeparator;
@property(nonatomic, retain) NSArray *rows;
@property(nonatomic, retain) NSMutableDictionary<NSString *, KuriPopoverButton *> *rowButtons;
@property(nonatomic, retain) NSMutableDictionary *feedback;
@property(nonatomic, retain) NSString *instance;
@property(nonatomic, retain) id keyMonitor;
@property(nonatomic, retain) id outsideMonitor;
@property(nonatomic, retain) id resignObserver;
@property(nonatomic, assign) NSStatusBarButton *anchor;
@property(nonatomic, retain) NSPanel *recentPanel;
@property(nonatomic, retain) KuriPopoverCanvas *recentContent;
@property(nonatomic, retain) NSTextField *emptyRecent;
@property(nonatomic) NSInteger recentFocus;
@property(nonatomic) BOOL recentAvailable;
@property(nonatomic) BOOL recentEnabled;
@property(nonatomic, retain) KuriRecentMenuButton *bookmarkButton;
@property(nonatomic, retain) NSTextField *bookmarkArrow;
@property(nonatomic, retain) NSPanel *bookmarkPanel;
@property(nonatomic, retain) KuriBookmarkMenuView *bookmarkMenu;
@property(nonatomic) NSUInteger bookmarkRequest;
- (void)layoutBookmarks;
- (void)scheduleBookmarkClose:(id)sender;
@end

static const CGFloat width = 346;
static const CGFloat padding = 13;
static const CGFloat menuRowHeight = 24;
static const CGFloat recentInset = 8;

static NSImage *recentMenuMask(void) {
    CGFloat size = 2 * recentCornerRadius + 1;
    NSImage *mask = [NSImage imageWithSize:NSMakeSize(size, size) flipped:NO drawingHandler:^BOOL(NSRect rect) {
        [NSColor.whiteColor setFill];
        [[NSBezierPath bezierPathWithRoundedRect:rect xRadius:recentCornerRadius yRadius:recentCornerRadius] fill];
        return YES;
    }];
    mask.capInsets = NSEdgeInsetsMake(recentCornerRadius, recentCornerRadius, recentCornerRadius, recentCornerRadius);
    mask.resizingMode = NSImageResizingModeStretch;
    return mask;
}

static NSTextField *label(NSString *text, NSFont *font, NSColor *color) {
    NSTextField *field = [NSTextField labelWithString:text];
    field.font = font;
    field.textColor = color;
    field.lineBreakMode = NSLineBreakByTruncatingTail;
    return field;
}

static NSBox *line(void) {
    NSBox *box = [[[NSBox alloc] init] autorelease];
    box.boxType = NSBoxSeparator;
    return box;
}

static KuriPopoverButton *rowButton(NSString *title, id target, SEL action, Class buttonClass) {
    KuriPopoverButton *button = [[[buttonClass alloc] initWithFrame:NSZeroRect] autorelease];
    button.target = target;
    button.action = action;
    button.bordered = NO;
    button.alignment = NSTextAlignmentLeft;
    button.font = [NSFont systemFontOfSize:13];
    button.lineBreakMode = NSLineBreakByTruncatingTail;
    button.tracksHover = YES;
    [button setRowTitle:title];
    return button;
}

@interface KuriBookmarkPanel : NSPanel
@end

@implementation KuriBookmarkPanel
- (BOOL)canBecomeKeyWindow { return YES; }
@end

static NSPanel *serviceMenuPanel(Class panelClass) {
    NSPanel *panel = [[[panelClass alloc] initWithContentRect:NSMakeRect(0, 0, 280, 40)
        styleMask:NSWindowStyleMaskBorderless | NSWindowStyleMaskNonactivatingPanel
        backing:NSBackingStoreBuffered defer:NO] autorelease];
    panel.releasedWhenClosed = NO;
    panel.hasShadow = YES;
    panel.opaque = NO;
    panel.backgroundColor = NSColor.clearColor;
    NSVisualEffectView *surface = [[[NSVisualEffectView alloc] initWithFrame:panel.contentView.bounds] autorelease];
    surface.material = NSVisualEffectMaterialMenu;
    surface.blendingMode = NSVisualEffectBlendingModeBehindWindow;
    surface.state = NSVisualEffectStateActive;
    surface.maskImage = recentMenuMask();
    panel.contentView = surface;
    return panel;
}

NSRect kuriServiceMenuFrame(NSRect parent, NSRect anchor, NSRect usable, NSSize size) {
    CGFloat paneWidth = MIN(size.width, NSWidth(usable)), height = MIN(size.height, NSHeight(usable));
    CGFloat x = NSMaxX(parent) + 3;
    if (x + paneWidth > NSMaxX(usable)) x = NSMinX(parent) - paneWidth - 3;
    x = MAX(NSMinX(usable), MIN(x, NSMaxX(usable) - paneWidth));
    CGFloat y = MAX(NSMinY(usable), MIN(NSMaxY(anchor) + recentInset - height, NSMaxY(usable) - height));
    return NSMakeRect(x, y, paneWidth, height);
}

static BOOL copyServiceURL(NSString *url, NSPasteboard *clipboard) {
    if (!url.length) return NO;
    [clipboard clearContents];
    return [clipboard setString:url forType:NSPasteboardTypeString];
}

@interface KuriBookmarkMenuView : KuriRecentMenuCanvas <NSSearchFieldDelegate>
@property(nonatomic, assign) KuriPopover *owner;
@property(nonatomic, retain) NSSearchField *search;
@property(nonatomic, retain) NSBox *separator;
@property(nonatomic, retain) NSScrollView *list;
@property(nonatomic, retain) NSTextField *status;
@property(nonatomic, copy) NSArray<NSDictionary *> *rows;
@property(nonatomic, copy) NSArray<NSDictionary *> *visibleRows;
@property(nonatomic, copy) NSArray<KuriPopoverButton *> *buttons;
@property(nonatomic, retain) NSMutableDictionary<NSString *, NSUUID *> *feedback;
@property(nonatomic, copy) NSString *message;
@property(nonatomic) NSInteger focus;
- (void)displayRows:(NSArray *)rows message:(NSString *)message;
- (void)filter;
- (CGFloat)desiredHeight;
- (void)reset;
- (BOOL)handleKey:(NSEvent *)event;
@end

@implementation KuriBookmarkMenuView
static const CGFloat bookmarkListTop = 42;

- (instancetype)initWithFrame:(NSRect)frame {
    self = [super initWithFrame:frame];
    if (!self) return nil;
    self.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
    self.search = [[[NSSearchField alloc] initWithFrame:NSZeroRect] autorelease];
    self.search.placeholderString = @"Search...";
    self.search.delegate = self;
    [self.search setAccessibilityLabel:@"Search bookmarked services"];
    [self addSubview:self.search];
    self.separator = line();
    [self addSubview:self.separator];
    self.list = [[[NSScrollView alloc] initWithFrame:NSZeroRect] autorelease];
    self.list.drawsBackground = NO;
    self.list.hasVerticalScroller = YES;
    self.list.autohidesScrollers = YES;
    self.list.documentView = [[[KuriPopoverCanvas alloc] initWithFrame:NSZeroRect] autorelease];
    [self addSubview:self.list];
    self.status = label(@"", [NSFont systemFontOfSize:13], NSColor.secondaryLabelColor);
    [self addSubview:self.status];
    self.feedback = [NSMutableDictionary dictionary];
    self.focus = -1;
    NSTrackingArea *tracking = [[[NSTrackingArea alloc] initWithRect:NSZeroRect
        options:NSTrackingMouseEnteredAndExited | NSTrackingActiveAlways | NSTrackingInVisibleRect
        owner:self userInfo:nil] autorelease];
    [self addTrackingArea:tracking];
    return self;
}

- (void)mouseExited:(NSEvent *)event { [self.owner scheduleBookmarkClose:nil]; }

- (void)reset {
    self.search.stringValue = @"";
    [self.feedback removeAllObjects];
    [self displayRows:@[] message:@"Loading bookmarks..."];
}

- (void)displayRows:(NSArray *)rows message:(NSString *)message {
    self.rows = rows;
    self.message = message;
    [self filter];
}

- (void)filter {
    NSString *query = self.search.stringValue;
    NSArray *keys = @[@"name", @"context", @"namespace", @"service", @"port_name"];
    self.visibleRows = [self.rows filteredArrayUsingPredicate:[NSPredicate predicateWithBlock:^BOOL(NSDictionary *row, NSDictionary *bindings) {
        if (!query.length) return YES;
        for (NSString *key in keys) {
            if ([row[key] localizedCaseInsensitiveContainsString:query]) return YES;
        }
        NSString *contextLabel = [[row[@"label"] componentsSeparatedByString:@"."] lastObject];
        return [contextLabel localizedCaseInsensitiveContainsString:query];
    }]];
    for (KuriPopoverButton *button in self.buttons) [button removeFromSuperview];
    self.focus = -1;
    NSMutableArray *buttons = [NSMutableArray array];
    for (NSDictionary *row in self.visibleRows) {
        KuriPopoverButton *button = rowButton(@"", self, @selector(copyBookmark:), KuriPopoverButton.class);
        button.identifier = row[@"id"];
        button.secondaryTitle = [NSString stringWithFormat:@"(%@)", row[@"label"]];
        button.enabled = [row[@"url"] length] > 0;
        button.toolTip = row[@"url"] ?: row[@"unavailable"];
        [self renderRow:row button:button];
        [self.list.documentView addSubview:button];
        [buttons addObject:button];
    }
    self.buttons = buttons;
    self.status.hidden = self.visibleRows.count != 0;
    self.status.stringValue = self.message ?: (self.rows.count ? @"No matching bookmarks" : @"No bookmarked services");
    self.status.toolTip = self.status.stringValue;
    [self.list.contentView scrollToPoint:NSZeroPoint];
    [self setNeedsLayout:YES];
    [self.owner layoutBookmarks];
}

- (void)renderRow:(NSDictionary *)row button:(KuriPopoverButton *)button {
    NSString *name = row[@"name"], *secondary = button.secondaryTitle;
    NSString *title = name.length ? [NSString stringWithFormat:@"%@ %@", name, secondary] : secondary;
    [button setAccessibilityLabel:title];
    [button setRowTitle:self.feedback[row[@"id"]] ? @"Copied" : title];
}

- (CGFloat)desiredHeight {
    return bookmarkListTop + recentInset + menuRowHeight * MAX(1, MIN(self.visibleRows.count, 10));
}

- (void)layout {
    [super layout];
    CGFloat w = MAX(0, NSWidth(self.bounds) - 2 * recentInset);
    self.search.frame = NSMakeRect(recentInset, 8, w, 24);
    self.separator.frame = NSMakeRect(recentInset, 36, w, 1);
    self.list.frame = NSMakeRect(recentInset, bookmarkListTop, w, MAX(0, NSHeight(self.bounds) - bookmarkListTop - recentInset));
    self.status.frame = NSMakeRect(recentInset + 6, bookmarkListTop, MAX(0, w - 12), menuRowHeight);
    self.list.hidden = self.status.hidden == NO;
    self.list.documentView.frame = NSMakeRect(0, 0, self.list.contentSize.width, menuRowHeight * self.buttons.count);
    [self.buttons enumerateObjectsUsingBlock:^(KuriPopoverButton *button, NSUInteger index, BOOL *stop) {
        button.frame = NSMakeRect(0, menuRowHeight * index, NSWidth(self.list.documentView.bounds), menuRowHeight);
    }];
}

- (void)controlTextDidChange:(NSNotification *)note { [self filter]; }

- (void)moveFocus:(NSInteger)step {
    NSInteger index = self.focus;
    if (index < 0 && step < 0) index = self.buttons.count;
    for (NSInteger next = index + step; next >= 0 && next < (NSInteger)self.buttons.count; next += step) {
        KuriPopoverButton *button = self.buttons[next];
        if (!button.enabled) continue;
        if (self.focus >= 0) {
            KuriPopoverButton *previous = self.buttons[self.focus];
            previous.submenuOpen = NO;
            [self renderRow:self.visibleRows[self.focus] button:previous];
        }
        self.focus = next;
        button.submenuOpen = YES;
        [self renderRow:self.visibleRows[next] button:button];
        [self.list.documentView scrollRectToVisible:button.frame];
        return;
    }
}

- (BOOL)handleKey:(NSEvent *)event {
    if (event.modifierFlags & (NSEventModifierFlagCommand | NSEventModifierFlagControl | NSEventModifierFlagOption)) return NO;
    if ([(NSTextView *)self.search.currentEditor hasMarkedText]) return NO;
    if (event.keyCode == 125 || event.keyCode == 126) {
        [self moveFocus:event.keyCode == 126 ? -1 : 1];
        return YES;
    }
    if ((event.keyCode == 36 || event.keyCode == 76) && self.focus >= 0) {
        [self copyBookmark:self.buttons[self.focus]];
        return YES;
    }
    return NO;
}

- (void)copyBookmark:(KuriPopoverButton *)button {
    [self copyBookmark:button pasteboard:NSPasteboard.generalPasteboard];
}

- (void)copyBookmark:(KuriPopoverButton *)button pasteboard:(NSPasteboard *)pasteboard {
    NSUInteger position = [self.buttons indexOfObjectIdenticalTo:button];
    if (position == NSNotFound) return;
    NSDictionary *row = self.visibleRows[position];
    if (!copyServiceURL(row[@"url"], pasteboard)) return;
    NSString *key = row[@"id"];
    NSUUID *token = NSUUID.UUID;
    self.feedback[key] = token;
    [self renderRow:row button:button];
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, NSEC_PER_SEC), dispatch_get_main_queue(), ^{
        if (self.feedback[key] != token) return;
        [self.feedback removeObjectForKey:key];
        [self.visibleRows enumerateObjectsUsingBlock:^(NSDictionary *item, NSUInteger index, BOOL *stop) {
            if ([item[@"id"] isEqualToString:key]) {
                [self renderRow:item button:self.buttons[index]];
                *stop = YES;
            }
        }];
    });
}

- (void)dealloc {
    self.search.delegate = nil;
    [_search release];
    [_separator release];
    [_list release];
    [_status release];
    [_rows release];
    [_visibleRows release];
    [_buttons release];
    [_feedback release];
    [_message release];
    [super dealloc];
}
@end

@implementation KuriPopover
- (void)loadView {
    self.view = [[[KuriPopoverCanvas alloc] initWithFrame:NSMakeRect(0, 0, width, 146)] autorelease];
    NSFont *regular = [NSFont systemFontOfSize:13];
    NSColor *secondary = [NSColor secondaryLabelColor];

    NSTextField *heading = label(@"Kuri", [NSFont boldSystemFontOfSize:15], [NSColor labelColor]);
    heading.frame = NSMakeRect(padding, 10, width - 94, 21);
    [self.view addSubview:heading];
    self.serverStatus = label(@"Checking...", regular, secondary);
    self.serverStatus.frame = NSMakeRect(padding, 31, width - 94, 19);
    [self.view addSubview:self.serverStatus];
    self.serverSwitch = [[[NSSwitch alloc] initWithFrame:NSMakeRect(width - 59, 16, 46, 23)] autorelease];
    [self.serverSwitch setAccessibilityLabel:@"Kuri server"];
    self.serverSwitch.target = self;
    self.serverSwitch.action = @selector(toggleServer:);
    self.serverSwitch.enabled = NO;
    [self.view addSubview:self.serverSwitch];

    self.headerLine = line();
    self.headerLine.frame = NSMakeRect(padding, 57, width - 2 * padding, 1);
    self.uptime = label(@"", regular, secondary);
    self.count = label(@"", regular, secondary);
    self.rowsLine = line();
    self.problem = label(@"", [NSFont systemFontOfSize:12], [NSColor systemRedColor]);
    self.quitLine = line();
    self.quitSeparator = line();
    self.bookmarkButton = (KuriRecentMenuButton *)rowButton(@"Bookmarked Services", self,
        @selector(focusBookmarks:), KuriRecentMenuButton.class);
    self.bookmarkButton.hoverAction = @selector(showBookmarks:);
    self.bookmarkButton.leaveAction = @selector(scheduleBookmarkClose:);
    self.bookmarkArrow = [KuriShortcutLabel labelWithString:@"\u203a"];
    self.bookmarkArrow.font = [NSFont systemFontOfSize:18];
    self.bookmarkArrow.textColor = secondary;
    self.bookmarkArrow.alignment = NSTextAlignmentRight;
    self.recentButton = rowButton(@"Recent Services", self, @selector(toggleRecent:), KuriRecentMenuButton.class);
    self.recentButton.hoverAction = @selector(showRecent:);
    self.recentButton.leaveAction = @selector(scheduleRecentClose:);
    self.recentArrow = [KuriShortcutLabel labelWithString:@"\u203a"];
    self.recentArrow.font = [NSFont systemFontOfSize:18];
    self.recentArrow.textColor = secondary;
    self.recentArrow.alignment = NSTextAlignmentRight;
    self.settingsButton = rowButton(@"Settings...", self, @selector(openSettings:), KuriPopoverButton.class);
    self.settingsShortcut = [KuriShortcutLabel labelWithString:@"\u2318 ,"];
    self.settingsShortcut.font = regular;
    self.settingsShortcut.textColor = secondary;
    self.settingsShortcut.alignment = NSTextAlignmentRight;
    self.quitButton = rowButton(@"Quit", self, @selector(quit:), KuriPopoverButton.class);
    self.shortcut = [KuriShortcutLabel labelWithString:@"\u2318 Q"];
    self.shortcut.font = regular;
    self.shortcut.textColor = secondary;
    self.shortcut.alignment = NSTextAlignmentRight;
    for (NSView *view in @[self.headerLine, self.uptime, self.count, self.rowsLine, self.bookmarkButton, self.bookmarkArrow,
                           self.recentButton, self.recentArrow, self.problem, self.quitLine,
                           self.settingsButton, self.settingsShortcut, self.quitSeparator, self.quitButton, self.shortcut]) {
        [self.view addSubview:view];
    }
    self.rowButtons = [NSMutableDictionary dictionary];
    self.feedback = [NSMutableDictionary dictionary];
    self.recentFocus = -1;
    [[NSNotificationCenter defaultCenter] addObserver:self selector:@selector(bookmarkListChanged:)
        name:KuriBookmarksDidChange object:nil];
}

- (void)toggleAt:(NSStatusBarButton *)button {
    if (self.popover.isShown) {
        [self close];
        return;
    }
    if (!self.popover) {
        self.popover = [[[NSPopover alloc] init] autorelease];
        self.popover.behavior = NSPopoverBehaviorApplicationDefined;
        self.popover.contentViewController = self;
        self.popover.delegate = self;
        self.popover.contentSize = self.view.frame.size;
    }
    self.anchor = button;
    [self.popover showRelativeToRect:button.bounds ofView:button preferredEdge:NSMinYEdge];
}

- (void)close {
    [self closeRecent];
    [self closeBookmarks];
    [self.popover close];
}

- (void)popoverDidShow:(NSNotification *)notification {
    if (@available(macOS 14.0, *)) {
        [NSApp activate];
    } else {
        [NSApp activateIgnoringOtherApps:YES];
    }
    [self.view.window makeKeyWindow];
    self.keyMonitor = [NSEvent addLocalMonitorForEventsMatchingMask:NSEventMaskKeyDown |
        NSEventMaskLeftMouseDown | NSEventMaskRightMouseDown handler:^NSEvent *(NSEvent *event) {
        if (event.type == NSEventTypeKeyDown) return [self handleKey:event];
        if (event.window != self.view.window && event.window != self.recentPanel &&
            event.window != self.bookmarkPanel && event.window != self.anchor.window)
            [self close];
        return event;
    }];
    self.outsideMonitor = [NSEvent addGlobalMonitorForEventsMatchingMask:NSEventMaskLeftMouseDown |
        NSEventMaskRightMouseDown handler:^(NSEvent *event) { [self close]; }];
    self.resignObserver = [[NSNotificationCenter defaultCenter] addObserverForName:NSApplicationDidResignActiveNotification
        object:NSApp queue:[NSOperationQueue mainQueue] usingBlock:^(NSNotification *note) { [self close]; }];
    kuriGoVisible(1);
}

- (void)popoverDidClose:(NSNotification *)notification {
    [self closeRecent];
    [self closeBookmarks];
    if (self.keyMonitor) {
        [NSEvent removeMonitor:self.keyMonitor];
        self.keyMonitor = nil;
    }
    if (self.outsideMonitor) {
        [NSEvent removeMonitor:self.outsideMonitor];
        self.outsideMonitor = nil;
    }
    if (self.resignObserver) {
        [[NSNotificationCenter defaultCenter] removeObserver:self.resignObserver];
        self.resignObserver = nil;
    }
    [self.feedback removeAllObjects];
    self.quitButton.hovered = NO;
    [self.quitButton setRowTitle:@"Quit"];
    for (NSDictionary *row in self.rows) {
        KuriPopoverButton *button = self.rowButtons[row[@"id"]];
        button.hovered = NO;
        [button setRowTitle:row[@"label"]];
    }
    kuriGoVisible(0);
}

- (void)applyView:(NSDictionary *)view {
    (void)self.view;
    BOOL known = [view[@"known"] boolValue];
    BOOL busy = [view[@"busy"] boolValue], quitting = [view[@"quitting"] boolValue];
    NSInteger pid = [view[@"pid"] integerValue];
    NSString *mode = view[@"mode"];
    BOOL running = pid > 0;
    NSString *newInstance = [NSString stringWithFormat:@"%ld/%@", (long)pid, view[@"started"] ?: @""];
    if (![self.instance isEqualToString:newInstance]) {
        self.instance = newInstance;
        [self.feedback removeAllObjects];
    }

    self.serverStatus.stringValue = running ? [NSString stringWithFormat:@"Running... (%ld)", (long)pid] :
        (known ? @"Stopped" : (busy ? @"Checking..." : @"Unavailable"));
    self.serverSwitch.state = running ? NSControlStateValueOn : NSControlStateValueOff;
    self.serverSwitch.enabled = known && !busy && !quitting && ![mode isEqualToString:@"foreground"];

    CGFloat y = 66;
    self.uptime.hidden = !running || !view[@"uptime"];
    if (!self.uptime.hidden) {
        self.uptime.stringValue = [NSString stringWithFormat:@"Started %@ ago", view[@"uptime"]];
        self.uptime.frame = NSMakeRect(padding, y, width - 2 * padding, 18);
        y += 20;
    }
    NSInteger connected = [view[@"connected"] integerValue];
    self.count.stringValue = [NSString stringWithFormat:@"Connected %ld %@", (long)connected, connected == 1 ? @"service" : @"services"];
    self.count.hidden = !known && !running;
    if (!self.count.hidden) {
        self.count.frame = NSMakeRect(padding, y, width - 2 * padding, 18);
        y += 24;
    }

    id rows = view[@"rows"];
    NSArray *next = [rows isKindOfClass:[NSArray class]] ? rows : @[];
    self.recentAvailable = known && [view[@"recent_available"] boolValue];
    self.recentEnabled = known && !quitting;
    self.recentButton.enabled = self.recentEnabled;
    self.bookmarkButton.enabled = !quitting;
    if (![self.rows isEqualToArray:next]) [self rebuildRows:next];
    self.rowsLine.frame = NSMakeRect(padding, y, width - 2 * padding, 1);
    y += 5;
    self.bookmarkButton.frame = NSMakeRect(padding, y, width - 2 * padding, menuRowHeight);
    self.bookmarkArrow.frame = NSMakeRect(width - 36, y - 2, 15, menuRowHeight);
    y += menuRowHeight;
    self.recentButton.frame = NSMakeRect(padding, y, width - 2 * padding, menuRowHeight);
    self.recentArrow.frame = NSMakeRect(width - 36, y - 2, 15, menuRowHeight);
    y += menuRowHeight + 3;
    for (NSDictionary *row in self.rows) {
        KuriPopoverButton *button = self.rowButtons[row[@"id"]];
        button.enabled = self.recentEnabled && [row[@"url"] length] != 0;
        [button setRowTitle:self.feedback[row[@"id"]] ? @"Copied" : row[@"label"]];
    }

    NSString *problem = view[@"error"];
    if (!problem.length && quitting) problem = @"Quitting...";
    if (!problem.length && busy) problem = @"Working...";
    if (!problem.length && [mode isEqualToString:@"foreground"]) problem = @"Stop the server in its terminal.";
    self.problem.hidden = !problem.length;
    if (problem.length) {
        self.problem.stringValue = problem;
        self.problem.toolTip = problem;
        self.problem.textColor = [view[@"error"] length] ? [NSColor systemRedColor] : [NSColor secondaryLabelColor];
        self.problem.frame = NSMakeRect(padding, y + 2, width - 2 * padding, 18);
        y += 23;
    }
    self.quitLine.frame = NSMakeRect(padding, y, width - 2 * padding, 1);
    self.settingsButton.frame = NSMakeRect(padding, y + 4, width - 2 * padding, menuRowHeight);
    self.settingsShortcut.frame = NSMakeRect(width - 60, y + 8, 47, 18);
    self.quitSeparator.frame = NSMakeRect(padding, y + 31, width - 2 * padding, 1);
    self.quitButton.frame = NSMakeRect(padding, y + 36, width - 2 * padding, menuRowHeight);
    self.shortcut.frame = NSMakeRect(width - 60, y + 40, 47, 18);
    NSSize size = NSMakeSize(width, y + 66);
    [self.view setFrameSize:size];
    if (!NSEqualSizes(self.popover.contentSize, size)) self.popover.contentSize = size;
    if (self.recentPanel.isVisible) {
        if (!self.recentEnabled) [self closeRecent];
        else [self layoutRecent];
    }
    if (self.bookmarkPanel.isVisible) {
        if (quitting) [self closeBookmarks];
        else [self layoutBookmarks];
    }
}

- (void)rebuildRows:(NSArray *)rows {
    for (NSButton *button in self.rowButtons.allValues) [button removeFromSuperview];
    [self.rowButtons removeAllObjects];
    self.rows = rows;
    self.recentFocus = -1;
    if (!self.recentPanel) return;
    [self addRecentRows];
}

- (void)addRecentRows {
    for (NSDictionary *row in self.rows) {
        KuriPopoverButton *button = rowButton(row[@"label"], self, @selector(copyForward:), KuriPopoverButton.class);
        button.identifier = row[@"id"];
        button.toolTip = [row[@"url"] length] ? row[@"url"] : @"No verified URL for this port forward.";
        button.leaveAction = @selector(scheduleRecentClose:);
        button.enabled = self.recentEnabled && [row[@"url"] length] != 0;
        [self.recentContent addSubview:button];
        self.rowButtons[button.identifier] = button;
    }
    for (NSString *key in self.feedback.allKeys) {
        if (!self.rowButtons[key]) [self.feedback removeObjectForKey:key];
    }
}

- (void)toggleRecent:(id)sender {
    if (!self.recentPanel.isVisible) [self showRecent:sender];
}

- (void)showRecent:(id)sender {
    if (!self.popover.isShown || !self.recentEnabled) return;
    [self closeBookmarks];
    if (!self.recentPanel) {
        self.recentPanel = serviceMenuPanel(NSPanel.class);
        NSView *surface = self.recentPanel.contentView;
        self.recentContent = [[[KuriRecentMenuCanvas alloc] initWithFrame:surface.bounds] autorelease];
        self.recentContent.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
        [surface addSubview:self.recentContent];
        self.emptyRecent = label(@"", [NSFont systemFontOfSize:13], NSColor.secondaryLabelColor);
        [self.recentContent addSubview:self.emptyRecent];
        NSTrackingArea *tracking = [[[NSTrackingArea alloc] initWithRect:NSZeroRect
            options:NSTrackingMouseEnteredAndExited | NSTrackingActiveAlways | NSTrackingInVisibleRect
            owner:self userInfo:nil] autorelease];
        [self.recentContent addTrackingArea:tracking];
        [self addRecentRows];
    }
    [self layoutRecent];
    if (!self.recentPanel.isVisible) {
        [self.view.window addChildWindow:self.recentPanel ordered:NSWindowAbove];
        [self.recentPanel orderFront:nil];
    }
    self.recentButton.hovered = YES;
    self.recentButton.submenuOpen = YES;
    [self.recentButton setRowTitle:@"Recent Services"];
    self.recentArrow.textColor = NSColor.selectedMenuItemTextColor;
}

- (NSRect)anchorRectForButton:(NSButton *)button {
    NSRect frame = [button convertRect:button.bounds toView:nil];
    return [self.view.window convertRectToScreen:frame];
}

- (NSRect)recentAnchorRect { return [self anchorRectForButton:self.recentButton]; }

- (void)layoutRecent {
    NSScreen *screen = self.view.window.screen ?: NSScreen.mainScreen;
    if (!screen) return;
    CGFloat height = 2 * recentInset + menuRowHeight * MAX(1, self.rows.count);
    NSRect frame = kuriServiceMenuFrame(self.view.window.frame, [self recentAnchorRect], screen.visibleFrame, NSMakeSize(310, height));
    [self.recentPanel setFrame:frame display:YES];
    CGFloat paneWidth = NSWidth(frame);
    self.emptyRecent.stringValue = self.recentAvailable ? @"No recent services" : @"Recent activity unavailable";
    self.emptyRecent.frame = NSMakeRect(recentInset, recentInset, paneWidth - 2 * recentInset, menuRowHeight);
    self.emptyRecent.hidden = self.rows.count != 0;
    [self.rows enumerateObjectsUsingBlock:^(NSDictionary *row, NSUInteger index, BOOL *stop) {
        self.rowButtons[row[@"id"]].frame = NSMakeRect(recentInset, recentInset + menuRowHeight * index,
                                                       paneWidth - 2 * recentInset, menuRowHeight);
    }];
}

- (void)closeRecent {
    if (!self.recentPanel.isVisible) return;
    [self.recentPanel.parentWindow removeChildWindow:self.recentPanel];
    [self.recentPanel orderOut:nil];
    self.recentFocus = -1;
    for (KuriPopoverButton *button in self.rowButtons.allValues) {
        if (!button.hovered) continue;
        button.hovered = NO;
        [button setRowTitle:button.title];
    }
    self.recentButton.hovered = NO;
    self.recentButton.submenuOpen = NO;
    [self.recentButton setRowTitle:@"Recent Services"];
    self.recentArrow.textColor = NSColor.secondaryLabelColor;
}

- (void)mouseExited:(NSEvent *)event { [self scheduleRecentClose:nil]; }

- (void)scheduleRecentClose:(id)sender {
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 160 * NSEC_PER_MSEC), dispatch_get_main_queue(), ^{
        if (!self.recentPanel.isVisible || self.recentFocus >= 0) return;
        NSPoint pointer = NSEvent.mouseLocation;
        if (!NSPointInRect(pointer, [self recentAnchorRect]) && !NSPointInRect(pointer, self.recentPanel.frame))
            [self closeRecent];
    });
}

- (void)moveRecentFocus:(NSInteger)step {
    NSInteger index = self.recentFocus;
    if (index < 0 && step < 0) index = (NSInteger)self.rows.count;
    for (NSInteger next = index + step; next >= 0 && next < (NSInteger)self.rows.count; next += step) {
        KuriPopoverButton *candidate = self.rowButtons[self.rows[next][@"id"]];
        if (!candidate.enabled) continue;
        if (self.recentFocus >= 0) {
            KuriPopoverButton *previous = self.rowButtons[self.rows[self.recentFocus][@"id"]];
            previous.hovered = NO;
            [previous setRowTitle:previous.title];
        }
        self.recentFocus = next;
        candidate.hovered = YES;
        [candidate setRowTitle:candidate.title];
        [self.recentPanel makeFirstResponder:candidate];
        return;
    }
}

- (NSEvent *)handleKey:(NSEvent *)event {
    if (!self.popover.isShown || (event.window != self.view.window && event.window != self.recentPanel &&
        event.window != self.bookmarkPanel)) return event;
    if (kuriCommandKey(event, KuriKeyQ, @"q")) { [self quit:nil]; return nil; }
    NSEventModifierFlags keys = NSEventModifierFlagCommand | NSEventModifierFlagShift |
        NSEventModifierFlagOption | NSEventModifierFlagControl;
    if ((event.modifierFlags & keys) == NSEventModifierFlagCommand) {
        NSString *key = event.charactersIgnoringModifiers.lowercaseString;
        if ([key isEqualToString:@","]) { [self openSettings:nil]; return nil; }
        return event;
    }
    if (event.keyCode == 53) {
        if (self.bookmarkPanel.isVisible) {
            if ([(NSTextView *)self.bookmarkMenu.search.currentEditor hasMarkedText]) return event;
            [self closeBookmarks];
            [self.view.window makeFirstResponder:self.bookmarkButton];
        } else if (self.recentPanel.isVisible) [self closeRecent];
        else [self close];
        return nil;
    }
    if (event.window == self.bookmarkPanel) return [self.bookmarkMenu handleKey:event] ? nil : event;
    if (self.view.window.firstResponder == self.bookmarkButton &&
        (event.keyCode == 124 || event.keyCode == 125 || event.keyCode == 36 || event.keyCode == 76)) {
        [self focusBookmarks:nil];
        return nil;
    }
    if (self.recentPanel.isVisible || self.view.window.firstResponder == self.recentButton) {
        if (event.keyCode == 125 || event.keyCode == 126 || (event.keyCode == 124 && !self.recentPanel.isVisible)) {
            [self showRecent:nil];
            [self moveRecentFocus:event.keyCode == 126 ? -1 : 1];
            return nil;
        }
        if (event.keyCode == 123 && self.recentPanel.isVisible) {
            [self closeRecent];
            [self.view.window makeFirstResponder:self.recentButton];
            return nil;
        }
        if (event.keyCode == 36 || event.keyCode == 76) {
            if (self.recentFocus >= 0) {
                KuriPopoverButton *button = self.rowButtons[self.rows[self.recentFocus][@"id"]];
                if (button.enabled) [self copyForward:button];
            } else {
                [self showRecent:nil];
                [self moveRecentFocus:1];
            }
            return nil;
        }
    }
    return event;
}

- (void)copyForward:(KuriPopoverButton *)sender {
    for (NSDictionary *row in self.rows) {
        if (![row[@"id"] isEqualToString:sender.identifier] || ![row[@"url"] length]) continue;
        if (!copyServiceURL(row[@"url"], NSPasteboard.generalPasteboard)) return;
        NSString *key = sender.identifier;
        NSString *token = [[NSUUID UUID] UUIDString];
        self.feedback[key] = token;
        [sender setRowTitle:@"Copied"];
        dispatch_after(dispatch_time(DISPATCH_TIME_NOW, NSEC_PER_SEC), dispatch_get_main_queue(), ^{
            if (![self.feedback[key] isEqualToString:token]) return;
            [self.feedback removeObjectForKey:key];
            for (NSDictionary *current in self.rows) {
                if ([current[@"id"] isEqualToString:key]) [self.rowButtons[key] setRowTitle:current[@"label"]];
            }
        });
        return;
    }
}

- (void)showBookmarks:(id)sender {
    if (!self.popover.isShown || !self.bookmarkButton.enabled || self.bookmarkPanel.isVisible) return;
    [self closeRecent];
    if (!self.bookmarkPanel) {
        self.bookmarkPanel = serviceMenuPanel(KuriBookmarkPanel.class);
        self.bookmarkMenu = [[[KuriBookmarkMenuView alloc] initWithFrame:self.bookmarkPanel.contentView.bounds] autorelease];
        self.bookmarkMenu.owner = self;
        [self.bookmarkPanel.contentView addSubview:self.bookmarkMenu];
    }
    [self.bookmarkMenu reset];
    [self layoutBookmarks];
    [self.view.window addChildWindow:self.bookmarkPanel ordered:NSWindowAbove];
    [self.bookmarkPanel orderFront:nil];
    self.bookmarkButton.submenuOpen = YES;
    [self.bookmarkButton setRowTitle:self.bookmarkButton.title];
    self.bookmarkArrow.textColor = NSColor.selectedMenuItemTextColor;
    [self reloadBookmarks];
}

- (void)focusBookmarks:(id)sender {
    [self showBookmarks:sender];
    if (!self.bookmarkPanel.isVisible) return;
    [self.bookmarkPanel makeKeyWindow];
    [self.bookmarkPanel makeFirstResponder:self.bookmarkMenu.search];
}

- (void)layoutBookmarks {
    NSScreen *screen = self.view.window.screen ?: NSScreen.mainScreen;
    if (!screen) return;
    NSRect anchor = [self anchorRectForButton:self.bookmarkButton];
    NSRect frame = kuriServiceMenuFrame(self.view.window.frame, anchor, screen.visibleFrame,
        NSMakeSize(340, self.bookmarkMenu.desiredHeight));
    if (!NSEqualRects(frame, self.bookmarkPanel.frame)) [self.bookmarkPanel setFrame:frame display:YES];
}

- (void)reloadBookmarks {
    NSUInteger request = ++self.bookmarkRequest;
    kuriRequestBookmarks(@{@"operation": @"menu"}, ^(NSDictionary *response) {
        if (request != self.bookmarkRequest || !self.bookmarkPanel.isVisible) return;
        NSArray *rows = [response[@"rows"] isKindOfClass:NSArray.class] ? response[@"rows"] : @[];
        [self.bookmarkMenu displayRows:rows message:response[@"error"]];
    });
}

- (void)bookmarkListChanged:(NSNotification *)notification {
    if (self.bookmarkPanel.isVisible) [self reloadBookmarks];
}

- (void)closeBookmarks {
    if (!self.bookmarkPanel.isVisible) return;
    ++self.bookmarkRequest;
    BOOL wasKey = self.bookmarkPanel.isKeyWindow;
    [self.bookmarkPanel makeFirstResponder:nil];
    [self.bookmarkPanel.parentWindow removeChildWindow:self.bookmarkPanel];
    [self.bookmarkPanel orderOut:nil];
    if (wasKey && self.popover.isShown) [self.view.window makeKeyWindow];
    self.bookmarkButton.hovered = NO;
    self.bookmarkButton.submenuOpen = NO;
    [self.bookmarkButton setRowTitle:self.bookmarkButton.title];
    self.bookmarkArrow.textColor = NSColor.secondaryLabelColor;
}

- (void)scheduleBookmarkClose:(id)sender {
    NSUInteger request = self.bookmarkRequest;
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 160 * NSEC_PER_MSEC), dispatch_get_main_queue(), ^{
        if (request != self.bookmarkRequest || !self.bookmarkPanel.isVisible || self.bookmarkMenu.search.currentEditor) return;
        NSPoint pointer = NSEvent.mouseLocation;
        if (!NSPointInRect(pointer, [self anchorRectForButton:self.bookmarkButton]) &&
            !NSPointInRect(pointer, self.bookmarkPanel.frame)) [self closeBookmarks];
    });
}

- (void)toggleServer:(NSSwitch *)sender {
    BOOL wantsStart = sender.state == NSControlStateValueOn;
    // Keep the last confirmed state until the command and its state query complete.
    sender.state = wantsStart ? NSControlStateValueOff : NSControlStateValueOn;
    sender.enabled = NO;
    kuriGoAction(wantsStart ? 1 : 2);
}

- (void)openSettings:(id)sender { kuriOpenSettings(); }
- (void)quit:(id)sender { kuriRequestQuit(); }
@end
