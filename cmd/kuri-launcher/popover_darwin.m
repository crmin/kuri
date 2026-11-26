//go:build cgo

#import "popover_darwin.h"

extern void kuriGoVisible(int visible);
extern void kuriGoAction(int action);

@interface KuriPopoverCanvas : NSView
@end

@implementation KuriPopoverCanvas
- (BOOL)isFlipped { return YES; }
@end

@interface KuriShortcutLabel : NSTextField
@end

@implementation KuriShortcutLabel
- (NSView *)hitTest:(NSPoint)point { return nil; }
@end

static NSColor *primaryColor(NSView *view) {
    NSAppearanceName appearance = [view.effectiveAppearance bestMatchFromAppearancesWithNames:@[NSAppearanceNameAqua, NSAppearanceNameDarkAqua]];
    return [appearance isEqualToString:NSAppearanceNameDarkAqua] ? [NSColor whiteColor] : [NSColor labelColor];
}

@interface KuriPopoverButton : NSButton
@property(nonatomic) BOOL tracksHover;
@property(nonatomic) BOOL hovered;
@property(nonatomic, retain) NSTrackingArea *rowTrackingArea;
- (void)setRowTitle:(NSString *)title;
@end

@implementation KuriPopoverButton
- (BOOL)showsHover { return self.tracksHover && self.hovered && self.enabled; }

- (void)setRowTitle:(NSString *)title {
    NSColor *color = [self showsHover] ? [NSColor controlAccentColor] : primaryColor(self);
    NSMutableParagraphStyle *paragraph = [[[NSMutableParagraphStyle alloc] init] autorelease];
    paragraph.alignment = NSTextAlignmentLeft;
    paragraph.lineBreakMode = NSLineBreakByTruncatingTail;
    self.attributedTitle = [[[NSAttributedString alloc] initWithString:title attributes:@{
        NSForegroundColorAttributeName: color, NSFontAttributeName: self.font,
        NSParagraphStyleAttributeName: paragraph,
    }] autorelease];
    self.needsDisplay = YES;
}
- (void)drawRect:(NSRect)dirtyRect {
    if ([self showsHover]) {
        [[[NSColor controlAccentColor] colorWithAlphaComponent:0.16] setFill];
        [[NSBezierPath bezierPathWithRoundedRect:NSInsetRect(self.bounds, 0, 1) xRadius:4 yRadius:4] fill];
    }
    [super drawRect:dirtyRect];
}
- (void)updateTrackingAreas {
    [super updateTrackingAreas];
    if (self.tracksHover && !self.rowTrackingArea) {
        self.rowTrackingArea = [[[NSTrackingArea alloc] initWithRect:NSZeroRect
            options:NSTrackingMouseEnteredAndExited | NSTrackingActiveInKeyWindow | NSTrackingInVisibleRect
            owner:self userInfo:nil] autorelease];
        [self addTrackingArea:self.rowTrackingArea];
    }
}
- (void)mouseEntered:(NSEvent *)event {
    self.hovered = YES;
    [self setRowTitle:self.title];
}
- (void)mouseExited:(NSEvent *)event {
    self.hovered = NO;
    [self setRowTitle:self.title];
}
- (void)viewDidChangeEffectiveAppearance {
    [super viewDidChangeEffectiveAppearance];
    if (self.font) [self setRowTitle:self.title];
}
- (void)dealloc {
    [_rowTrackingArea release];
    [super dealloc];
}
@end

@interface KuriPopover ()
@property(nonatomic, retain) NSPopover *popover;
@property(nonatomic, retain) NSTextField *serverStatus;
@property(nonatomic, retain) NSSwitch *serverSwitch;
@property(nonatomic, retain) NSTextField *uptime;
@property(nonatomic, retain) NSTextField *count;
@property(nonatomic, retain) NSTextField *unavailable;
@property(nonatomic, retain) NSTextField *problem;
@property(nonatomic, retain) KuriPopoverButton *quitButton;
@property(nonatomic, retain) NSTextField *shortcut;
@property(nonatomic, retain) NSBox *headerLine;
@property(nonatomic, retain) NSBox *rowsLine;
@property(nonatomic, retain) NSBox *quitLine;
@property(nonatomic, retain) NSArray *rows;
@property(nonatomic, retain) NSMutableDictionary *rowButtons;
@property(nonatomic, retain) NSMutableDictionary *feedback;
@property(nonatomic, retain) NSString *instance;
@property(nonatomic, retain) id keyMonitor;
@end

static const CGFloat width = 346;
static const CGFloat padding = 13;

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

static KuriPopoverButton *rowButton(NSString *title, id target, SEL action, BOOL tracksHover) {
    KuriPopoverButton *button = [[[KuriPopoverButton alloc] initWithFrame:NSZeroRect] autorelease];
    button.target = target;
    button.action = action;
    button.bordered = NO;
    button.alignment = NSTextAlignmentLeft;
    button.font = [NSFont systemFontOfSize:13];
    button.lineBreakMode = NSLineBreakByTruncatingTail;
    button.tracksHover = tracksHover;
    [button setRowTitle:title];
    return button;
}

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
    self.unavailable = label(@"Recent activity unavailable", regular, secondary);
    self.problem = label(@"", [NSFont systemFontOfSize:12], [NSColor systemRedColor]);
    self.quitLine = line();
    self.quitButton = rowButton(@"Quit", self, @selector(quit:), YES);
    self.shortcut = [KuriShortcutLabel labelWithString:@"\u2318 Q"];
    self.shortcut.font = regular;
    self.shortcut.textColor = secondary;
    self.shortcut.alignment = NSTextAlignmentRight;
    for (NSView *view in @[self.headerLine, self.uptime, self.count, self.rowsLine,
                           self.unavailable, self.problem, self.quitLine, self.quitButton, self.shortcut]) {
        [self.view addSubview:view];
    }
    self.rowButtons = [NSMutableDictionary dictionary];
    self.feedback = [NSMutableDictionary dictionary];
}

- (void)toggleAt:(NSStatusBarButton *)button {
    if (self.popover.isShown) {
        [self close];
        return;
    }
    if (!self.popover) {
        self.popover = [[[NSPopover alloc] init] autorelease];
        self.popover.behavior = NSPopoverBehaviorTransient;
        self.popover.contentViewController = self;
        self.popover.delegate = self;
        self.popover.contentSize = self.view.frame.size;
    }
    [self.popover showRelativeToRect:button.bounds ofView:button preferredEdge:NSMinYEdge];
}

- (void)close { [self.popover close]; }

- (void)popoverDidShow:(NSNotification *)notification {
    if (@available(macOS 14.0, *)) {
        [NSApp activate];
    } else {
        [NSApp activateIgnoringOtherApps:YES];
    }
    [self.view.window makeKeyWindow];
    self.keyMonitor = [NSEvent addLocalMonitorForEventsMatchingMask:NSEventMaskKeyDown handler:^NSEvent *(NSEvent *event) {
        NSEventModifierFlags keys = NSEventModifierFlagCommand | NSEventModifierFlagShift |
            NSEventModifierFlagOption | NSEventModifierFlagControl;
        if (self.popover.isShown && event.window == self.view.window &&
            (event.modifierFlags & keys) == NSEventModifierFlagCommand &&
            [[event.charactersIgnoringModifiers lowercaseString] isEqualToString:@"q"]) {
            [self quit:nil];
            return nil;
        }
        return event;
    }];
    kuriGoVisible(1);
}

- (void)popoverDidClose:(NSNotification *)notification {
    if (self.keyMonitor) {
        [NSEvent removeMonitor:self.keyMonitor];
        self.keyMonitor = nil;
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
    if (![self.rows isEqualToArray:next]) [self rebuildRows:next];
    self.unavailable.hidden = !known || connected == 0 || [view[@"recent_available"] boolValue];
    self.rowsLine.hidden = next.count == 0 && self.unavailable.hidden;
    if (!self.rowsLine.hidden) {
        self.rowsLine.frame = NSMakeRect(padding, y, width - 2 * padding, 1);
        y += 5;
    }
    if (!self.unavailable.hidden) {
        self.unavailable.frame = NSMakeRect(padding, y, width - 2 * padding, 20);
        y += 23;
    }
    for (NSDictionary *row in self.rows) {
        KuriPopoverButton *button = self.rowButtons[row[@"id"]];
        button.frame = NSMakeRect(padding, y, width - 2 * padding, 24);
        button.enabled = known && !quitting && [row[@"url"] length] != 0;
        [button setRowTitle:self.feedback[row[@"id"]] ? @"Copied" : row[@"label"]];
        y += 24;
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
    self.quitButton.frame = NSMakeRect(padding, y + 4, width - 2 * padding, 24);
    self.shortcut.frame = NSMakeRect(width - 60, y + 8, 47, 18);
    NSSize size = NSMakeSize(width, y + 34);
    [self.view setFrameSize:size];
    if (!NSEqualSizes(self.popover.contentSize, size)) self.popover.contentSize = size;
}

- (void)rebuildRows:(NSArray *)rows {
    for (NSButton *button in self.rowButtons.allValues) [button removeFromSuperview];
    [self.rowButtons removeAllObjects];
    self.rows = rows;
    for (NSDictionary *row in rows) {
        KuriPopoverButton *button = rowButton(row[@"label"], self, @selector(copyForward:), YES);
        button.identifier = row[@"id"];
        button.toolTip = [row[@"url"] length] ? row[@"url"] : @"No verified URL for this port forward.";
        [self.view addSubview:button];
        self.rowButtons[button.identifier] = button;
    }
    for (NSString *key in self.feedback.allKeys) {
        if (!self.rowButtons[key]) [self.feedback removeObjectForKey:key];
    }
}

- (void)copyForward:(KuriPopoverButton *)sender {
    for (NSDictionary *row in self.rows) {
        if (![row[@"id"] isEqualToString:sender.identifier] || ![row[@"url"] length]) continue;
        NSPasteboard *clipboard = [NSPasteboard generalPasteboard];
        [clipboard clearContents];
        if (![clipboard setString:row[@"url"] forType:NSPasteboardTypeString]) return;
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

- (void)toggleServer:(NSSwitch *)sender {
    BOOL wantsStart = sender.state == NSControlStateValueOn;
    // Keep the last confirmed state until the command and its state query complete.
    sender.state = wantsStart ? NSControlStateValueOff : NSControlStateValueOn;
    sender.enabled = NO;
    kuriGoAction(wantsStart ? 1 : 2);
}

- (void)quit:(id)sender { kuriGoAction(3); }
@end
