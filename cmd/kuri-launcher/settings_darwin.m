//go:build cgo

#import "settings_darwin.h"
#import "bookmarks_darwin.h"
#import "shortcuts_darwin.h"
#import <ServiceManagement/ServiceManagement.h>
#import <UniformTypeIdentifiers/UniformTypeIdentifiers.h>

extern void kuriGoSettingsRequest(char *edit);
extern void kuriGoAction(int action);

@interface KuriSettingsWindow : NSWindow
@end

@implementation KuriSettingsWindow
- (BOOL)performKeyEquivalent:(NSEvent *)event {
    if (self.isKeyWindow && !self.attachedSheet && kuriCommandKey(event, KuriKeyW, @"w")) {
        [self performClose:nil];
        return YES;
    }
    return [super performKeyEquivalent:event];
}
@end

@interface KuriSettingsCanvas : NSView
@end

@implementation KuriSettingsCanvas
- (BOOL)isFlipped { return YES; }
@end

static NSTextField *settingsLabel(NSString *text, NSColor *color) {
    NSTextField *field = [NSTextField wrappingLabelWithString:text];
    field.textColor = color;
    field.maximumNumberOfLines = 0;
    field.lineBreakMode = NSLineBreakByWordWrapping;
    [field setContentCompressionResistancePriority:NSLayoutPriorityDefaultLow forOrientation:NSLayoutConstraintOrientationHorizontal];
    return field;
}

static NSFont *settingsItemFont(void) {
    return [NSFont boldSystemFontOfSize:13];
}

static NSTextField *settingsItemLabel(NSString *title) {
    NSTextField *label = [NSTextField labelWithString:title];
    label.font = settingsItemFont();
    return label;
}

static NSStackView *settingsStack(NSUserInterfaceLayoutOrientation orientation, CGFloat gap) {
    NSStackView *stack = [NSStackView stackViewWithViews:@[]];
    stack.orientation = orientation;
    stack.alignment = orientation == NSUserInterfaceLayoutOrientationVertical
        ? NSLayoutAttributeLeading : NSLayoutAttributeCenterY;
    stack.spacing = gap;
    stack.distribution = NSStackViewDistributionFill;
    return stack;
}

static NSScrollView *settingsScroll(NSStackView **content, CGFloat inset, CGFloat gap, NSBorderType border) {
    NSScrollView *scroll = [[[NSScrollView alloc] initWithFrame:NSZeroRect] autorelease];
    scroll.hasVerticalScroller = YES;
    scroll.autohidesScrollers = YES;
    scroll.borderType = border;
    KuriSettingsCanvas *document = [[[KuriSettingsCanvas alloc] initWithFrame:NSZeroRect] autorelease];
    document.translatesAutoresizingMaskIntoConstraints = NO;
    scroll.documentView = document;
    NSStackView *stack = settingsStack(NSUserInterfaceLayoutOrientationVertical, gap);
    stack.translatesAutoresizingMaskIntoConstraints = NO;
    [document addSubview:stack];
    [NSLayoutConstraint activateConstraints:@[
        [document.widthAnchor constraintEqualToAnchor:scroll.contentView.widthAnchor],
        [stack.leadingAnchor constraintEqualToAnchor:document.leadingAnchor constant:inset],
        [stack.trailingAnchor constraintEqualToAnchor:document.trailingAnchor constant:-inset],
        [stack.topAnchor constraintEqualToAnchor:document.topAnchor constant:inset],
        [stack.bottomAnchor constraintEqualToAnchor:document.bottomAnchor constant:-inset],
    ]];
    *content = stack;
    return scroll;
}

static void addFullWidth(NSStackView *stack, NSView *view) {
    [stack addArrangedSubview:view];
    [view.widthAnchor constraintEqualToAnchor:stack.widthAnchor].active = YES;
}

static NSString *settingsString(id value) {
    if ([value isKindOfClass:[NSString class]]) return value;
    if ([value isKindOfClass:[NSNumber class]]) return [value stringValue];
    return @"";
}

@interface KuriSettings () <NSToolbarDelegate>
@property(nonatomic, retain) NSTabView *tabs;
@property(nonatomic, retain) KuriBookmarkSettings *bookmarks;
@property(nonatomic, retain) NSStackView *contexts;
@property(nonatomic, retain) NSTextField *path;
@property(nonatomic, retain) NSTextField *problem;
@property(nonatomic, retain) NSTextField *serverError;
@property(nonatomic, retain) NSTextField *contextError;
@property(nonatomic, retain) NSTextField *forwardError;
@property(nonatomic, retain) NSButton *prepareProxy;
@property(nonatomic, retain) NSButton *autoStart;
@property(nonatomic, retain) NSButton *keepAlive;
@property(nonatomic, retain) NSButton *autoHealing;
@property(nonatomic, retain) NSButton *retry;
@property(nonatomic, retain) NSPopUpButton *trigger;
@property(nonatomic, retain) NSMutableArray<NSControl *> *controls;
@property(nonatomic, retain) NSMutableDictionary<NSString *, NSTextField *> *fields;
@property(nonatomic, retain) NSMutableDictionary<NSString *, NSButton *> *styles;
@property(nonatomic, retain) NSDictionary *saved;
@property(nonatomic, retain) NSDictionary *pending;
@property(nonatomic, retain) NSString *settingsPath;
@property(nonatomic, retain) NSString *kubeconfigFingerprint;
@property(nonatomic, retain) NSDictionary *contextAliases;
@property(nonatomic, retain) NSWindow *dialog;
@property(nonatomic, retain) NSDictionary *aliasTarget;
@property(nonatomic, retain) NSTextField *aliasField;
@property(nonatomic, retain) NSTextField *aliasError;
@property(nonatomic, retain) NSButton *aliasSave;
@property(nonatomic, retain) NSString *transferPath;
@property(nonatomic, retain) NSDictionary *transferPreview;
@property(nonatomic, retain) NSMutableDictionary<NSString *, NSButton *> *transferChoices;
@property(nonatomic, retain) NSButton *transferConfirm;
@property(nonatomic, retain) NSTextField *transferError;
@property(nonatomic) BOOL busy;
@property(nonatomic) BOOL loaded;
@property(nonatomic) BOOL corrupt;
@property(nonatomic) BOOL closeAfterSave;
@property(nonatomic) BOOL quitAfterSave;
@property(nonatomic) BOOL confirmingClose;
@property(nonatomic) BOOL portDraftUnapplied;
@end

@implementation KuriSettings
- (instancetype)init {
    NSWindow *window = [[[KuriSettingsWindow alloc] initWithContentRect:NSMakeRect(0, 0, 650, 660)
        styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable
        backing:NSBackingStoreBuffered defer:NO] autorelease];
    self = [super initWithWindow:window];
    if (!self) return nil;
    window.title = @"kuri Settings";
    window.delegate = self;
    window.releasedWhenClosed = NO;
    window.minSize = NSMakeSize(490, 410);
    self.controls = [NSMutableArray array];
    self.fields = [NSMutableDictionary dictionary];
    self.styles = [NSMutableDictionary dictionary];
    self.saved = @{};
    [self build];
    [window center];
    return self;
}

- (void)open {
    if (@available(macOS 14.0, *)) [NSApp activate];
    else [NSApp activateIgnoringOtherApps:YES];
    if (!self.window.isVisible) {
        self.loaded = NO;
        self.portDraftUnapplied = NO;
        self.saved = @{};
        self.serverError.hidden = YES;
        self.forwardError.hidden = YES;
        [self submit:nil];
        if ([self.tabs.selectedTabViewItem.identifier isEqual:@"Bookmark"]) [self.bookmarks reload];
    }
    [self.window makeKeyAndOrderFront:nil];
}

- (NSStackView *)addSection:(NSString *)name to:(NSStackView *)sections {
    NSStackView *row = settingsStack(NSUserInterfaceLayoutOrientationHorizontal, 16);
    row.alignment = NSLayoutAttributeTop;
    NSTextField *heading = [NSTextField wrappingLabelWithString:name];
    heading.font = [NSFont boldSystemFontOfSize:14];
    heading.alignment = NSTextAlignmentRight;
    [row addArrangedSubview:heading];
    [heading.widthAnchor constraintEqualToConstant:120].active = YES;

    NSStackView *section = settingsStack(NSUserInterfaceLayoutOrientationVertical, 9);
    [row addArrangedSubview:section];
    addFullWidth(sections, row);
    return section;
}

- (void)addDescription:(NSString *)text to:(NSStackView *)section {
    addFullWidth(section, settingsLabel(text, NSColor.secondaryLabelColor));
}

- (void)addLoginItemToSection:(NSStackView *)section {
    NSStackView *row = settingsStack(NSUserInterfaceLayoutOrientationHorizontal, 10);
    row.alignment = NSLayoutAttributeCenterY;
    [row addArrangedSubview:settingsItemLabel(@"Open at Login")];
    [row addArrangedSubview:[NSButton buttonWithTitle:@"Open System Settings" target:self action:@selector(openLoginItemsSettings:)]];
    [section addArrangedSubview:row];
    [self addDescription:@"Under Open at Login, click +, select kuri, and click Add." to:section];
}

- (void)openLoginItemsSettings:(id)sender {
    [SMAppService openSystemSettingsLoginItems];
}

- (void)updateProxyRegistration:(id)sender {
    [self submit:@{@"prepare_proxy": @YES}];
}

- (NSButton *)button:(NSString *)title action:(SEL)action {
    NSButton *button = [NSButton buttonWithTitle:title target:self action:action];
    [self.controls addObject:button];
    return button;
}

- (NSButton *)checkbox:(NSString *)title identifier:(NSString *)identifier inSection:(NSStackView *)section {
    NSButton *button = [NSButton checkboxWithTitle:title target:self action:@selector(toggle:)];
    button.identifier = identifier;
    button.font = settingsItemFont();
    button.alignment = NSTextAlignmentLeft;
    button.usesSingleLineMode = YES;
    button.lineBreakMode = NSLineBreakByTruncatingMiddle;
    [button setContentCompressionResistancePriority:NSLayoutPriorityDefaultLow forOrientation:NSLayoutConstraintOrientationHorizontal];
    [self.controls addObject:button];
    addFullWidth(section, button);
    return button;
}

- (void)field:(NSString *)name identifier:(NSString *)identifier width:(CGFloat)width unit:(NSString *)unit
    inSection:(NSStackView *)section {
    NSStackView *row = settingsStack(NSUserInterfaceLayoutOrientationHorizontal, 9);
    NSTextField *label = settingsItemLabel(name);
    if ([identifier isEqualToString:@"check_seconds"]) {
        label = settingsLabel(name, NSColor.labelColor);
        label.font = settingsItemFont();
    }
    NSTextField *field = [[[NSTextField alloc] initWithFrame:NSZeroRect] autorelease];
    [field setAccessibilityLabel:name];
    field.identifier = identifier;
    field.maximumNumberOfLines = 1;
    [row addArrangedSubview:label];
    [label.widthAnchor constraintEqualToConstant:132].active = YES;
    [row addArrangedSubview:field];
    if (width > 0) [field.widthAnchor constraintEqualToConstant:width].active = YES;
    if (unit) [row addArrangedSubview:[NSTextField labelWithString:unit]];
    if (width > 0) [section addArrangedSubview:row];
    else addFullWidth(section, row);
    [self.controls addObject:field];
    self.fields[identifier] = field;
}

- (void)build {
    NSStackView *body = settingsStack(NSUserInterfaceLayoutOrientationVertical, 4);
    self.window.contentView = body;
    self.problem = settingsLabel(@"", NSColor.systemRedColor);
    self.problem.hidden = YES;
    addFullWidth(body, self.problem);
    self.retry = [self button:@"Retry Applying Settings" action:@selector(retry:)];
    self.retry.hidden = YES;
    [body addArrangedSubview:self.retry];

    self.tabs = [[[NSTabView alloc] initWithFrame:NSZeroRect] autorelease];
    self.tabs.tabViewType = NSNoTabsNoBorder;
    self.tabs.drawsBackground = NO;
    addFullWidth(body, self.tabs);
    NSToolbar *toolbar = [[[NSToolbar alloc] initWithIdentifier:@"KuriSettingsTabs"] autorelease];
    toolbar.delegate = self;
    toolbar.displayMode = NSToolbarDisplayModeIconAndLabel;
    self.window.toolbarStyle = NSWindowToolbarStylePreference;
    self.window.toolbar = toolbar;
    self.window.autorecalculatesKeyViewLoop = YES;

    NSStackView *sections = [self addTab:@"General"];
    NSStackView *section = [self addSection:@"General" to:sections];
    [self addLoginItemToSection:section];
    self.autoStart = [self checkbox:@"Start the reverse proxy when kuri opens" identifier:@"auto_start" inSection:section];
    self.prepareProxy = [self button:@"Update Proxy Registration" action:@selector(updateProxyRegistration:)];
    self.prepareProxy.hidden = YES;
    [section addArrangedSubview:self.prepareProxy];

    section = [self addSection:@"Kubernetes" to:sections];
    NSStackView *pathRow = settingsStack(NSUserInterfaceLayoutOrientationHorizontal, 9);
    [pathRow addArrangedSubview:settingsItemLabel(@"Kubeconfig Path")];
    self.path = [NSTextField labelWithString:@""];
    self.path.font = [NSFont monospacedSystemFontOfSize:12 weight:NSFontWeightRegular];
    self.path.lineBreakMode = NSLineBreakByTruncatingMiddle;
    [self.path setContentCompressionResistancePriority:NSLayoutPriorityDefaultLow forOrientation:NSLayoutConstraintOrientationHorizontal];
    [pathRow addArrangedSubview:self.path];
    [pathRow addArrangedSubview:[self button:@"Open..." action:@selector(openConfig:)]];
    addFullWidth(section, pathRow);
    [section addArrangedSubview:settingsItemLabel(@"Contexts")];
    [self addDescription:@"The reverse proxy routes requests only to the Kubernetes contexts you enable here." to:section];
    self.contextError = settingsLabel(@"", NSColor.systemRedColor);
    self.contextError.hidden = YES;
    addFullWidth(section, self.contextError);
    NSStackView *contexts = nil;
    NSScrollView *contextScroll = settingsScroll(&contexts, 8, 3, NSBezelBorder);
    self.contexts = contexts;
    [contextScroll.heightAnchor constraintEqualToConstant:145].active = YES;
    addFullWidth(section, contextScroll);

    sections = [self addTab:@"Bookmark"];
    self.bookmarks = [[[KuriBookmarkSettings alloc] initWithSettings:self] autorelease];
    addFullWidth(sections, self.bookmarks.view);

    sections = [self addTab:@"Network"];
    section = [self addSection:@"Server" to:sections];
    [section addArrangedSubview:settingsItemLabel(@"Host")];
    [self addDescription:@"If another service uses port 80, choose a different port. To keep using port 80, configure that service to forward requests to kuri's new port."
                      to:section];
    [self field:@"Service Domain" identifier:@"domain" width:0 unit:nil inSection:section];
    [self field:@"Port" identifier:@"port" width:85 unit:nil inSection:section];
    self.serverError = settingsLabel(@"", NSColor.systemRedColor);
    self.serverError.hidden = YES;
    addFullWidth(section, self.serverError);
    [section addArrangedSubview:[self button:@"Save Server Settings" action:@selector(saveServer:)]];
    [section setCustomSpacing:18 afterView:section.arrangedSubviews.lastObject];
    [section addArrangedSubview:settingsItemLabel(@"Domain Styles")];
    for (NSString *style in @[@"explicit_context", @"explicit_port", @"current_context"]) {
        NSButton *button = [self checkbox:@"" identifier:style inSection:section];
        self.styles[style] = button;
    }

    section = [self addSection:@"Port Forwarding" to:sections];
    [section addArrangedSubview:settingsItemLabel(@"Keep-Alive")];
    [self addDescription:@"Keep the port forward open after the first request so later requests can connect faster." to:section];
    self.keepAlive = [self checkbox:@"Enable port forwarding keep-alive" identifier:@"keep_alive" inSection:section];
    [self field:@"Keep-alive duration" identifier:@"minutes" width:85 unit:@"minutes" inSection:section];
    [section addArrangedSubview:settingsItemLabel(@"Start the keep-alive timer from")];
    self.trigger = [[[NSPopUpButton alloc] initWithFrame:NSZeroRect pullsDown:NO] autorelease];
    [self.trigger addItemsWithTitles:@[@"Last request", @"When the port forward opens"]];
    self.trigger.target = self;
    self.trigger.action = @selector(changeTrigger:);
    [self.trigger setAccessibilityLabel:@"Start the keep-alive timer from"];
    [self.controls addObject:self.trigger];
    [section addArrangedSubview:self.trigger];
    [section setCustomSpacing:18 afterView:self.trigger];
    [section addArrangedSubview:settingsItemLabel(@"Auto Healing")];
    [self addDescription:@"Check open port forwards at regular intervals. If a reusable connection fails, kuri tries to restore it." to:section];
    self.autoHealing = [self checkbox:@"Enable auto healing" identifier:@"auto_healing" inSection:section];
    [self field:@"Connection check interval" identifier:@"check_seconds" width:72 unit:@"seconds" inSection:section];
    [section setCustomSpacing:18 afterView:section.arrangedSubviews.lastObject];
    [section addArrangedSubview:settingsItemLabel(@"Port Range")];
    [self addDescription:@"kuri uses ports in this range for port forwarding. If no port is available, kuri closes its oldest open port forward to make room for a new one, even when keep-alive is enabled."
                      to:section];
    [self field:@"Min" identifier:@"min_port" width:85 unit:nil inSection:section];
    [self field:@"Max" identifier:@"max_port" width:85 unit:nil inSection:section];
    self.forwardError = settingsLabel(@"", NSColor.systemRedColor);
    self.forwardError.hidden = YES;
    addFullWidth(section, self.forwardError);
    [section addArrangedSubview:[self button:@"Save Port Forwarding Settings" action:@selector(saveForward:)]];
    sections = [self addTab:@"About"];
    [self addAppInfoTo:sections];
    section = [self addSection:@"Settings Import/Export" to:sections];
    NSStackView *transfer = settingsStack(NSUserInterfaceLayoutOrientationHorizontal, 8);
    [transfer addArrangedSubview:[self button:@"Import settings" action:@selector(openImport:)]];
    [transfer addArrangedSubview:[self button:@"Export settings" action:@selector(openExport:)]];
    [section addArrangedSubview:transfer];
    section = [self addSection:@"Logs" to:sections];
    NSStackView *logs = settingsStack(NSUserInterfaceLayoutOrientationHorizontal, 8);
    [logs addArrangedSubview:[NSButton buttonWithTitle:@"Show Logs" target:self action:@selector(showLogs:)]];
    [logs addArrangedSubview:[NSButton buttonWithTitle:@"Open Log Directory" target:self action:@selector(openLogDirectory:)]];
    [section addArrangedSubview:logs];
    section = [self addSection:@"Reset" to:sections];
    [section addArrangedSubview:[self button:@"Reset Settings..." action:@selector(reset:)]];
    [self selectTab:@"General"];
    [self updateControls];
}

- (NSStackView *)addTab:(NSString *)title {
    NSStackView *sections = nil;
    NSTabViewItem *item = [[[NSTabViewItem alloc] initWithIdentifier:title] autorelease];
    item.label = title;
    item.view = settingsScroll(&sections, 24, 24, NSNoBorder);
    [self.tabs addTabViewItem:item];
    return sections;
}

- (NSArray *)toolbarAllowedItemIdentifiers:(NSToolbar *)toolbar {
    return @[@"General", @"Bookmark", @"Network", @"About"];
}

- (NSArray *)toolbarDefaultItemIdentifiers:(NSToolbar *)toolbar { return [self toolbarAllowedItemIdentifiers:toolbar]; }
- (NSArray *)toolbarSelectableItemIdentifiers:(NSToolbar *)toolbar { return [self toolbarAllowedItemIdentifiers:toolbar]; }

- (NSToolbarItem *)toolbar:(NSToolbar *)toolbar itemForItemIdentifier:(NSString *)identifier
    willBeInsertedIntoToolbar:(BOOL)inserted {
    NSString *symbol = @{@"General": @"gearshape", @"Bookmark": @"bookmark", @"Network": @"network",
        @"About": @"info.circle"}[identifier];
    NSToolbarItem *item = [[[NSToolbarItem alloc] initWithItemIdentifier:identifier] autorelease];
    item.label = identifier;
    item.image = [NSImage imageWithSystemSymbolName:symbol accessibilityDescription:identifier];
    item.target = self;
    item.action = @selector(changeTab:);
    return item;
}

- (void)changeTab:(NSToolbarItem *)item { [self selectTab:item.itemIdentifier]; }

- (void)selectTab:(NSString *)identifier {
    [self.tabs selectTabViewItemWithIdentifier:identifier];
    self.window.toolbar.selectedItemIdentifier = identifier;
    if ([identifier isEqualToString:@"Bookmark"]) [self.bookmarks reload];
}

- (void)addAppInfoTo:(NSStackView *)sections {
    NSBundle *bundle = NSBundle.mainBundle;
    NSString *name = [bundle objectForInfoDictionaryKey:@"CFBundleDisplayName"] ?: @"kuri";
    NSString *version = [bundle objectForInfoDictionaryKey:@"KuriVersion"];
    NSStackView *row = settingsStack(NSUserInterfaceLayoutOrientationHorizontal, 24);
    row.alignment = NSLayoutAttributeTop;
    NSImageView *icon = [NSImageView imageViewWithImage:NSApp.applicationIconImage];
    icon.imageScaling = NSImageScaleProportionallyUpOrDown;
    [icon.widthAnchor constraintEqualToConstant:96].active = YES;
    [icon.heightAnchor constraintEqualToConstant:96].active = YES;
    [row addArrangedSubview:icon];
    NSStackView *info = settingsStack(NSUserInterfaceLayoutOrientationVertical, 6);
    NSTextField *appName = settingsItemLabel(name);
    appName.font = [NSFont boldSystemFontOfSize:17];
    [info addArrangedSubview:appName];
    if (version.length) {
        [info addArrangedSubview:settingsLabel([@"Version " stringByAppendingString:version], NSColor.secondaryLabelColor)];
    }
    NSButton *repository = [NSButton buttonWithTitle:@"Repository" target:self action:@selector(openRepository:)];
    repository.bordered = NO;
    repository.attributedTitle = [[[NSAttributedString alloc] initWithString:@"Repository"
        attributes:@{NSForegroundColorAttributeName: NSColor.linkColor}] autorelease];
    [info addArrangedSubview:repository];
    [row addArrangedSubview:info];
    addFullWidth(sections, row);
}

- (void)openRepository:(id)sender {
    [NSWorkspace.sharedWorkspace openURL:[NSURL URLWithString:@"https://github.com/crmin/kuri"]];
}

- (void)submit:(NSDictionary *)edit {
    if (self.busy || self.bookmarks.saving) return;
    self.pending = edit;
    self.busy = YES;
    [self updateControls];
    if (!edit) {
        kuriGoSettingsRequest(NULL);
        return;
    }
    if (edit[@"port"] && ![self.saved[@"port"] isEqualToString:edit[@"port"]]) {
        self.serverError.textColor = NSColor.secondaryLabelColor;
        self.serverError.stringValue = @"Restarting the reverse proxy...";
        self.serverError.hidden = NO;
    }
    NSData *data = [NSJSONSerialization dataWithJSONObject:edit options:0 error:NULL];
    NSString *json = [[[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding] autorelease];
    kuriGoSettingsRequest((char *)json.UTF8String);
}

- (void)updateControls {
    for (NSControl *control in self.controls) {
        BOOL reset = [control isKindOfClass:[NSButton class]] && control.action == @selector(reset:);
        control.enabled = !self.busy && !self.dialog && self.loaded && (!self.corrupt || reset);
    }
    self.fields[@"minutes"].enabled = self.fields[@"minutes"].enabled && self.keepAlive.state == NSControlStateValueOn;
    self.fields[@"check_seconds"].enabled = self.fields[@"check_seconds"].enabled && self.autoHealing.state == NSControlStateValueOn;
    self.trigger.enabled = self.trigger.enabled && self.keepAlive.state == NSControlStateValueOn;
    for (NSStackView *row in self.contexts.arrangedSubviews) {
        NSSwitch *toggle = (NSSwitch *)row.arrangedSubviews.lastObject;
        toggle.enabled = self.loaded && !self.busy && !self.corrupt && !self.dialog;
        NSButton *alias = (NSButton *)row.arrangedSubviews[1];
        alias.enabled = toggle.enabled;
    }
    self.aliasSave.enabled = !self.busy;
    self.transferConfirm.enabled = !self.busy && [self selectedTransferFields].count > 0;
    [self.bookmarks setAvailable:!self.busy && !self.dialog && !self.window.attachedSheet];
}

- (NSDictionary *)draft {
    NSMutableDictionary *draft = [NSMutableDictionary dictionary];
    for (NSString *key in self.fields) {
        NSTextField *field = self.fields[key];
        NSString *text = field.currentEditor ? field.currentEditor.string : field.stringValue;
        if (![text isEqualToString:self.saved[key] ?: @""] || ([key isEqual:@"port"] && self.portDraftUnapplied))
            draft[key] = text;
    }
    return draft;
}

- (void)applyResponse:(NSDictionary *)response {
    NSDictionary *pending = [[self.pending retain] autorelease];
    if (pending[@"transfer_operation"]) {
        [self applyTransferResponse:response pending:pending];
        return;
    }
    NSDictionary *draft = [self draft];
    BOOL saved = [response[@"saved"] boolValue];
    BOOL applied = [response[@"applied"] boolValue];
    NSString *portError = response[@"port_error"];
    NSString *error = response[@"error"];
    BOOL hostEdit = pending[@"domain"] || pending[@"port"];
    BOOL forwardEdit = pending[@"minutes"] || pending[@"min_port"] || pending[@"max_port"] || pending[@"check_seconds"];
    BOOL reset = [pending[@"reset"] boolValue];
    if (pending[@"port"] || (reset && applied)) self.portDraftUnapplied = portError.length != 0;
    if (portError.length || hostEdit || reset) {
        self.serverError.textColor = NSColor.systemRedColor;
        self.serverError.stringValue = portError ?: ((hostEdit && !saved) ? error ?: @"" : @"");
        self.serverError.hidden = !self.serverError.stringValue.length;
    }
    if (forwardEdit || reset) {
        self.forwardError.stringValue = (!hostEdit && forwardEdit && !saved) ? error ?: @"" : @"";
        self.forwardError.hidden = !self.forwardError.stringValue.length;
    }

    NSDictionary *cfg = response[@"config"];
    if ([cfg isKindOfClass:NSDictionary.class]) {
        self.loaded = YES;
        self.corrupt = [response[@"file_error"] length] != 0;
        self.prepareProxy.hidden = ![response[@"proxy_update_needed"] boolValue];
        NSDictionary *general = cfg[@"general"], *kube = cfg[@"kubernetes"];
        NSDictionary *server = cfg[@"server"], *forward = cfg[@"port_forward"];
        self.autoStart.state = [general[@"auto_start_proxy"] boolValue];
        self.keepAlive.state = [forward[@"keep_alive_enabled"] boolValue];
        self.autoHealing.state = [forward[@"auto_healing_enabled"] ?: @YES boolValue];
        [self.trigger selectItemAtIndex:[forward[@"keep_alive_trigger"] isEqual:@"forward_opened"] ? 1 : 0];
        self.saved = @{
            @"domain": settingsString(server[@"domain"]), @"port": settingsString(server[@"port"]),
            @"minutes": settingsString(forward[@"keep_alive_minutes"]),
            @"check_seconds": settingsString(forward[@"connection_check_interval_seconds"] ?: @10),
            @"min_port": settingsString(forward[@"min_port"]), @"max_port": settingsString(forward[@"max_port"]),
        };
        for (NSString *key in self.fields) {
            BOOL restoredPort = [key isEqual:@"port"] && portError.length != 0;
            BOOL disabledByEdit = ([key isEqual:@"minutes"] && pending[@"keep_alive"] && self.keepAlive.state == NSControlStateValueOff) ||
                ([key isEqual:@"check_seconds"] && pending[@"auto_healing"] && self.autoHealing.state == NSControlStateValueOff);
            BOOL acceptSaved = (saved && ((pending[key] && !restoredPort) || disabledByEdit)) || (reset && applied);
            NSString *text = restoredPort && pending[key] ? pending[key] :
                (!pending || acceptSaved) ? self.saved[key] : draft[key] ?: self.saved[key];
            self.fields[key].stringValue = text ?: @"";
        }

        self.settingsPath = settingsString(kube[@"config_path"]);
        self.kubeconfigFingerprint = settingsString(response[@"kubeconfig_fingerprint"]);
        self.contextAliases = kube[@"context_aliases"][self.settingsPath] ?: @{};
        NSString *displayPath = self.settingsPath;
        if (displayPath.length > 33) {
            NSRange head = [displayPath rangeOfComposedCharacterSequencesForRange:NSMakeRange(0, 15)];
            NSRange tail = [displayPath rangeOfComposedCharacterSequencesForRange:NSMakeRange(displayPath.length - 15, 15)];
            displayPath = [NSString stringWithFormat:@"%@...%@", [displayPath substringWithRange:head],
                [displayPath substringWithRange:tail]];
        }
        self.path.stringValue = displayPath;
        self.path.toolTip = self.settingsPath;
        [self.path setAccessibilityValue:self.settingsPath];
        self.contextError.stringValue = response[@"kubeconfig_error"] ?: @"";
        self.contextError.hidden = self.contextError.stringValue.length == 0;
        [self renderContexts:response[@"contexts"] excluded:kube[@"excluded_contexts"]];
        [self renderStyles:server];
    }

    NSString *notice = response[@"file_error"] ?: response[@"registration_error"] ?: response[@"runtime_warning"];
    if (error.length && !portError.length && (!(hostEdit || forwardEdit) || saved)) notice = error;
    if (saved && !applied && error.length && !portError.length) {
        notice = @"Settings saved. The reverse proxy could not apply the changes.";
        self.problem.toolTip = error;
        self.retry.hidden = NO;
    } else {
        self.retry.hidden = ![response[@"apply_pending"] boolValue] || (!portError.length && ![notice isEqual:response[@"runtime_warning"]]);
        self.problem.toolTip = notice;
    }
    self.problem.stringValue = notice ?: @"";
    self.problem.hidden = !notice.length;

    self.busy = NO;
    self.pending = nil;
    [self updateControls];
    if (saved) [[NSNotificationCenter defaultCenter] postNotificationName:KuriBookmarksDidChange object:nil];
    if (pending[@"context_aliases"] && self.aliasTarget) {
        if (saved) {
            [self closeDialog:nil];
        } else {
            self.aliasError.stringValue = error ?: @"Could not save the context alias.";
            self.aliasError.hidden = NO;
        }
    }
    if (portError.length || (error.length && !saved && (hostEdit || forwardEdit))) {
        [self selectTab:@"Network"];
        [self reveal:(hostEdit || portError.length) ? self.serverError : self.forwardError];
    }
    if (self.closeAfterSave) {
        BOOL quit = self.quitAfterSave;
        self.closeAfterSave = NO;
        self.quitAfterSave = NO;
        if (applied) [self dismiss:quit];
    }
}

- (void)reveal:(NSView *)view {
    [self.window.contentView layoutSubtreeIfNeeded];
    [view scrollRectToVisible:view.bounds];
}

- (void)renderContexts:(NSArray *)names excluded:(NSArray *)excluded {
    for (NSView *row in [NSArray arrayWithArray:self.contexts.arrangedSubviews]) {
        [self.contexts removeArrangedSubview:row];
        [row removeFromSuperview];
    }
    for (NSString *name in names) {
        NSStackView *row = settingsStack(NSUserInterfaceLayoutOrientationHorizontal, 9);
        NSStackView *names = settingsStack(NSUserInterfaceLayoutOrientationHorizontal, 5);
        NSTextField *title = settingsLabel(name, NSColor.labelColor);
        title.font = settingsItemFont();
        title.lineBreakMode = NSLineBreakByCharWrapping;
        [names addArrangedSubview:title];
        NSString *alias = self.contextAliases[name];
        if (alias.length && ![alias isEqualToString:name]) {
            NSTextField *aliasLabel = settingsLabel([NSString stringWithFormat:@"(%@)", alias], NSColor.secondaryLabelColor);
            aliasLabel.lineBreakMode = NSLineBreakByCharWrapping;
            [names addArrangedSubview:aliasLabel];
        }
        [row addArrangedSubview:names];
        NSButton *more = [NSButton buttonWithImage:[NSImage imageWithSystemSymbolName:@"ellipsis.circle" accessibilityDescription:nil]
            target:self action:@selector(openAlias:)];
        more.bordered = NO;
        more.identifier = name;
        more.toolTip = @"Edit context alias";
        [more setAccessibilityLabel:[@"Edit alias for " stringByAppendingString:name]];
        [more.widthAnchor constraintEqualToConstant:22].active = YES;
        [row addArrangedSubview:more];
        NSSwitch *toggle = [[[NSSwitch alloc] initWithFrame:NSZeroRect] autorelease];
        [toggle.widthAnchor constraintEqualToConstant:42].active = YES;
        [toggle setAccessibilityLabel:name];
        toggle.identifier = name;
        toggle.state = ![excluded containsObject:name];
        toggle.target = self;
        toggle.action = @selector(changeContext:);
        [row addArrangedSubview:toggle];
        addFullWidth(self.contexts, row);
    }
}

- (NSStackView *)createDialog:(NSString *)title size:(NSSize)size {
    NSWindow *window = [[[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, size.width, size.height)
        styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable backing:NSBackingStoreBuffered defer:NO] autorelease];
    window.title = title;
    window.delegate = self;
    window.releasedWhenClosed = NO;
    self.dialog = window;
    NSView *content = window.contentView;
    NSStackView *stack = settingsStack(NSUserInterfaceLayoutOrientationVertical, 10);
    stack.translatesAutoresizingMaskIntoConstraints = NO;
    [content addSubview:stack];
    [NSLayoutConstraint activateConstraints:@[
        [stack.leadingAnchor constraintEqualToAnchor:content.leadingAnchor constant:22],
        [stack.trailingAnchor constraintEqualToAnchor:content.trailingAnchor constant:-22],
        [stack.topAnchor constraintEqualToAnchor:content.topAnchor constant:20],
        [stack.bottomAnchor constraintLessThanOrEqualToAnchor:content.bottomAnchor constant:-20],
    ]];
    return stack;
}

- (void)showDialog {
    [self.dialog center];
    [self.window addChildWindow:self.dialog ordered:NSWindowAbove];
    [self updateControls];
    [self.dialog makeKeyAndOrderFront:nil];
}

- (NSStackView *)dialogActions:(NSString *)title action:(SEL)action in:(NSStackView *)stack {
    NSStackView *row = settingsStack(NSUserInterfaceLayoutOrientationHorizontal, 8);
    NSView *space = [[[NSView alloc] initWithFrame:NSZeroRect] autorelease];
    [row addArrangedSubview:space];
    NSButton *cancel = [NSButton buttonWithTitle:@"Cancel" target:self action:@selector(closeDialog:)];
    cancel.keyEquivalent = @"\e";
    [row addArrangedSubview:cancel];
    NSButton *confirm = [NSButton buttonWithTitle:title target:self action:action];
    confirm.keyEquivalent = @"\r";
    [row addArrangedSubview:confirm];
    addFullWidth(stack, row);
    return row;
}

- (void)openAlias:(NSButton *)sender {
    if (self.dialog || self.busy || !self.kubeconfigFingerprint.length) return;
    NSString *name = sender.identifier;
    self.aliasTarget = @{@"name": name, @"config_path": self.settingsPath, @"fingerprint": self.kubeconfigFingerprint};
    NSStackView *stack = [self createDialog:name size:NSMakeSize(460, 230)];
    [stack addArrangedSubview:settingsItemLabel(@"Alias")];
    [self addDescription:@"Use this alias in the context part of a service domain." to:stack];
    NSString *alias = self.contextAliases[name];
    if (alias.length) [self addDescription:[@"Current alias: " stringByAppendingString:alias] to:stack];
    self.aliasField = [NSTextField textFieldWithString:name];
    [self.aliasField setAccessibilityLabel:@"Alias"];
    addFullWidth(stack, self.aliasField);
    self.aliasError = settingsLabel(@"", NSColor.systemRedColor);
    self.aliasError.hidden = YES;
    addFullWidth(stack, self.aliasError);
    self.aliasSave = (NSButton *)[self dialogActions:@"Save" action:@selector(saveAlias:) in:stack].arrangedSubviews.lastObject;
    [self showDialog];
    [self.dialog makeFirstResponder:self.aliasField];
}

- (void)saveAlias:(id)sender {
    if (self.busy || !self.aliasTarget) return;
    if (![self.dialog makeFirstResponder:nil]) return;
    self.aliasError.hidden = YES;
    [self submit:@{@"context_aliases": @{@"config_path": self.aliasTarget[@"config_path"],
        @"fingerprint": self.aliasTarget[@"fingerprint"], @"aliases": @{self.aliasTarget[@"name"]: self.aliasField.stringValue}}}];
}

- (void)closeDialog:(id)sender {
    if (!self.dialog || self.busy || self.bookmarks.saving) return;
    [self.bookmarks endEditing];
    [self.window removeChildWindow:self.dialog];
    [self.dialog orderOut:nil];
    self.dialog.delegate = nil;
    self.dialog = nil;
    self.aliasTarget = nil;
    self.aliasField = nil;
    self.aliasError = nil;
    self.aliasSave = nil;
    self.transferChoices = nil;
    self.transferConfirm = nil;
    self.transferError = nil;
    self.transferPreview = nil;
    self.transferPath = nil;
    [self updateControls];
    [self.window makeKeyAndOrderFront:nil];
}

- (void)submitBookmark:(id)sender { [self.bookmarks addItem]; }

static NSArray *settingsTransferGroups(void) {
    return @[
        @[@"Server", @[@"domain", @"Service Domain"], @[@"port", @"Port"], @[@"domain_styles", @"Domain Styles"]],
        @[@"Port Forwarding", @[@"keep_alive_enabled", @"Enable port forwarding keep-alive"],
            @[@"keep_alive_minutes", @"Keep-alive duration"], @[@"keep_alive_trigger", @"Start the keep-alive timer from"],
            @[@"auto_healing_enabled", @"Enable auto healing"], @[@"connection_check_interval_seconds", @"Connection check interval"],
            @[@"port_range", @"Port Range"]],
        @[@"Kubernetes", @[@"context_aliases", @"Context Aliases"]],
    ];
}

- (void)openImport:(id)sender {
    if (self.dialog || self.busy) return;
    NSOpenPanel *panel = [NSOpenPanel openPanel];
    panel.canChooseFiles = YES;
    panel.canChooseDirectories = NO;
    panel.allowsMultipleSelection = NO;
    panel.allowedContentTypes = @[[UTType typeWithFilenameExtension:@"yaml"]];
    [panel beginSheetModalForWindow:self.window completionHandler:^(NSModalResponse result) {
        if (result == NSModalResponseOK) [self previewImport:panel.URL.path keepingSelection:nil];
    }];
}

- (void)previewImport:(NSString *)path keepingSelection:(NSArray *)selection {
    if (!path.length || [path.pathExtension caseInsensitiveCompare:@"yaml"] != NSOrderedSame) {
        [self notice:@"Choose a settings file with the .yaml extension."];
        return;
    }
    self.transferPath = path;
    NSMutableDictionary *request = [NSMutableDictionary dictionaryWithDictionary:@{@"transfer_operation": @"preview_import", @"path": path}];
    if (selection) request[@"fields"] = selection;
    [self submit:request];
}

- (void)openExport:(id)sender {
    if (self.dialog || self.busy) return;
    [self openTransferDialog:NO keepingSelection:nil];
}

- (void)openTransferDialog:(BOOL)importing keepingSelection:(NSArray *)selection {
    NSString *action = importing ? @"Import" : @"Export";
    NSStackView *stack = [self createDialog:[action stringByAppendingString:@" settings"] size:NSMakeSize(445, 575)];
    NSString *description = importing ? @"Choose the settings to import from this file." : @"Choose the settings to export to a YAML file.";
    if (selection) description = @"The active kubeconfig changed. Review your selection before importing.";
    [self addDescription:description to:stack];
    NSArray *fields = self.transferPreview[@"fields"];
    self.transferChoices = [NSMutableDictionary dictionary];
    for (NSArray *group in settingsTransferGroups()) {
        [stack addArrangedSubview:settingsItemLabel(group.firstObject)];
        NSStackView *choices = settingsStack(NSUserInterfaceLayoutOrientationVertical, 2);
        for (NSUInteger i = 1; i < group.count; i++) {
            NSArray *item = group[i];
            NSButton *checkbox = [NSButton checkboxWithTitle:item[1] target:self action:@selector(changeTransferSelection:)];
            checkbox.enabled = !importing || [fields containsObject:item[0]];
            checkbox.state = checkbox.enabled && (!selection || [selection containsObject:item[0]]) ? NSControlStateValueOn : NSControlStateValueOff;
            self.transferChoices[item[0]] = checkbox;
            [choices addArrangedSubview:checkbox];
        }
        addFullWidth(stack, choices);
    }
    NSStackView *bulk = settingsStack(NSUserInterfaceLayoutOrientationHorizontal, 8);
    [bulk addArrangedSubview:[NSButton buttonWithTitle:@"Select All" target:self action:@selector(selectAllTransfer:)]];
    [bulk addArrangedSubview:[NSButton buttonWithTitle:@"Deselect All" target:self action:@selector(deselectAllTransfer:)]];
    [stack addArrangedSubview:bulk];
    self.transferError = settingsLabel(@"", NSColor.systemRedColor);
    self.transferError.hidden = YES;
    addFullWidth(stack, self.transferError);
    self.transferConfirm = (NSButton *)[self dialogActions:action action:@selector(confirmTransfer:) in:stack].arrangedSubviews.lastObject;
    [self showDialog];
}

- (NSArray *)selectedTransferFields {
    NSMutableArray *fields = [NSMutableArray array];
    for (NSString *field in self.transferChoices) {
        NSButton *button = self.transferChoices[field];
        if (button.enabled && button.state == NSControlStateValueOn) [fields addObject:field];
    }
    return fields;
}

- (void)changeTransferSelection:(id)sender { [self updateControls]; }

- (void)setTransferSelection:(BOOL)on {
    if (self.busy) return;
    for (NSButton *button in self.transferChoices.allValues) {
        button.state = on && button.enabled ? NSControlStateValueOn : NSControlStateValueOff;
    }
    [self changeTransferSelection:nil];
}

- (void)selectAllTransfer:(id)sender { [self setTransferSelection:YES]; }
- (void)deselectAllTransfer:(id)sender { [self setTransferSelection:NO]; }

- (NSDictionary *)transferredDraftFields:(NSDictionary *)pending {
    NSArray *fields = pending[@"fields"];
    NSDictionary *transfer = pending[@"transfer"];
    NSDictionary *server = transfer[@"server"], *forward = transfer[@"port_forward"];
    NSMutableDictionary *draft = [NSMutableDictionary dictionary];
    for (NSString *key in @[@"domain", @"port"]) {
        if ([fields containsObject:key] && server[key]) draft[key] = settingsString(server[key]);
    }
    if ([fields containsObject:@"keep_alive_minutes"] && forward[@"keep_alive_minutes"])
        draft[@"minutes"] = settingsString(forward[@"keep_alive_minutes"]);
    if ([fields containsObject:@"connection_check_interval_seconds"] && forward[@"connection_check_interval_seconds"])
        draft[@"check_seconds"] = settingsString(forward[@"connection_check_interval_seconds"]);
    if ([fields containsObject:@"port_range"]) {
        for (NSString *key in @[@"min_port", @"max_port"]) {
            if (forward[key]) draft[key] = settingsString(forward[key]);
        }
    }
    return draft;
}

- (void)confirmTransfer:(id)sender {
    NSArray *fields = [self selectedTransferFields];
    if (self.busy || !fields.count) return;
    self.transferError.hidden = YES;
    if (!self.transferPreview) {
        NSSavePanel *panel = [NSSavePanel savePanel];
        panel.allowedContentTypes = @[[UTType typeWithFilenameExtension:@"yaml"]];
        panel.nameFieldStringValue = @"kuri-settings.yaml";
        const char *launchHome = getenv("HOME");
        NSString *home = launchHome ? [NSString stringWithUTF8String:launchHome] : NSHomeDirectory();
        panel.directoryURL = [NSURL fileURLWithPath:[home stringByAppendingPathComponent:@"Documents"] isDirectory:YES];
        [panel beginSheetModalForWindow:self.dialog completionHandler:^(NSModalResponse result) {
            if (result == NSModalResponseOK)
                [self submit:@{@"transfer_operation": @"export", @"path": panel.URL.path, @"fields": fields}];
        }];
        return;
    }
    NSDictionary *pending = @{@"transfer_operation": @"import", @"fields": fields,
        @"transfer": self.transferPreview[@"transfer"],
        @"kubeconfig_path": self.transferPreview[@"kubeconfig_path"] ?: @"",
        @"kubeconfig_fingerprint": self.transferPreview[@"kubeconfig_fingerprint"] ?: @""};
    NSDictionary *draft = [self draft];
    for (NSString *key in [self transferredDraftFields:pending]) {
        if (draft[key]) {
            NSAlert *alert = [[[NSAlert alloc] init] autorelease];
            alert.messageText = @"Replace unsaved changes?";
            alert.informativeText = @"Import will replace unsaved values for the selected settings.";
            [alert addButtonWithTitle:@"Import"];
            [alert addButtonWithTitle:@"Cancel"].keyEquivalent = @"\e";
            [alert beginSheetModalForWindow:self.dialog completionHandler:^(NSModalResponse result) {
                if (result == NSAlertFirstButtonReturn) [self submit:pending];
            }];
            return;
        }
    }
    [self submit:pending];
}

- (void)applyTransferResponse:(NSDictionary *)response pending:(NSDictionary *)pending {
    NSString *operation = pending[@"transfer_operation"];
    NSString *error = response[@"error"];
    self.busy = NO;
    self.pending = nil;
    [self updateControls];
    if ([operation isEqualToString:@"preview_import"]) {
        if (error.length) {
            [self notice:error];
        } else {
            self.transferPreview = response;
            [self openTransferDialog:YES keepingSelection:pending[@"fields"]];
        }
        return;
    }
    if ([response[@"refresh"] boolValue]) {
        NSString *path = [[self.transferPath retain] autorelease];
        [self closeDialog:nil];
        [self previewImport:path keepingSelection:pending[@"fields"]];
        return;
    }
    if (error.length) {
        self.transferError.stringValue = error;
        self.transferError.hidden = NO;
        return;
    }
    [self closeDialog:nil];
    if (response[@"settings"]) {
        self.pending = [self transferredDraftFields:pending];
        [self applyResponse:response[@"settings"]];
    }
}

- (void)renderStyles:(NSDictionary *)server {
    NSDictionary *templates = @{
        @"explicit_context": @"service.namespace.ctx",
        @"explicit_port": @"portName.service.namespace.ctx",
        @"current_context": @"service.namespace"
    };
    NSNumber *port = server[@"port"];
    NSString *suffix = port.integerValue == 80 ? [@"." stringByAppendingString:settingsString(server[@"domain"])] :
        [NSString stringWithFormat:@".%@:%@", settingsString(server[@"domain"]), settingsString(port)];
    NSMutableParagraphStyle *paragraph = [[[NSMutableParagraphStyle alloc] init] autorelease];
    paragraph.lineBreakMode = NSLineBreakByTruncatingMiddle;
    for (NSString *key in self.styles) {
        NSButton *button = self.styles[key];
        NSString *label = [templates[key] stringByAppendingString:suffix];
        NSMutableAttributedString *title = [[[NSMutableAttributedString alloc] initWithString:label attributes:
            @{NSForegroundColorAttributeName: NSColor.labelColor, NSFontAttributeName: button.font,
              NSParagraphStyleAttributeName: paragraph}] autorelease];
        [title addAttribute:NSForegroundColorAttributeName value:NSColor.secondaryLabelColor
            range:NSMakeRange(0, [templates[key] length])];
        button.attributedTitle = title;
        [button setAccessibilityLabel:label];
        button.toolTip = label;
        button.state = [server[@"domain_styles"][key] boolValue];
    }
}

- (void)toggle:(NSButton *)sender {
    NSString *key = sender.identifier;
    NSNumber *on = [NSNumber numberWithBool:(sender.state == NSControlStateValueOn)];
    if (self.styles[key]) [self submit:@{@"style": @{@"name": key, @"on": on}}];
    else [self submit:@{key: on}];
}

- (void)changeContext:(NSSwitch *)sender {
    NSNumber *on = [NSNumber numberWithBool:(sender.state == NSControlStateValueOn)];
    [self submit:@{@"context": @{@"name": sender.identifier, @"on": on}}];
}

- (void)changeTrigger:(NSPopUpButton *)sender {
    [self submit:@{@"trigger": sender.indexOfSelectedItem == 0 ? @"last_request" : @"forward_opened"}];
}

- (void)saveServer:(id)sender {
    [self.window endEditingFor:nil];
    [self submit:@{@"domain": self.fields[@"domain"].stringValue, @"port": self.fields[@"port"].stringValue}];
}

- (void)saveForward:(id)sender {
    [self.window endEditingFor:nil];
    NSMutableDictionary *edit = [@{@"min_port": self.fields[@"min_port"].stringValue,
                                  @"max_port": self.fields[@"max_port"].stringValue} mutableCopy];
    if (self.keepAlive.state == NSControlStateValueOn) edit[@"minutes"] = self.fields[@"minutes"].stringValue;
    if (self.autoHealing.state == NSControlStateValueOn) edit[@"check_seconds"] = self.fields[@"check_seconds"].stringValue;
    [self submit:edit];
    [edit release];
}

- (void)retry:(id)sender { [self submit:@{}]; }

- (void)openConfig:(id)sender {
    NSOpenPanel *panel = [NSOpenPanel openPanel];
    panel.canChooseDirectories = NO;
    panel.canChooseFiles = YES;
    panel.allowsMultipleSelection = NO;
    NSString *home = NSHomeDirectory();
    const char *launchHome = getenv("HOME");
    if (launchHome && launchHome[0] == '/') home = [NSString stringWithUTF8String:launchHome];
    NSString *defaultPath = [home stringByAppendingPathComponent:@".kube/config"];
    NSString *directory = [[NSFileManager defaultManager] fileExistsAtPath:defaultPath]
        ? defaultPath.stringByDeletingLastPathComponent : home;
    panel.directoryURL = [NSURL fileURLWithPath:directory isDirectory:YES];
    [panel beginSheetModalForWindow:self.window completionHandler:^(NSModalResponse result) {
        if (result == NSModalResponseOK) [self submit:@{@"config_path": panel.URL.path}];
    }];
}

- (NSString *)logsPath {
    const char *launchHome = getenv("HOME");
    NSString *home = launchHome && launchHome[0] == '/' ? [NSString stringWithUTF8String:launchHome] : NSHomeDirectory();
    return [home stringByAppendingPathComponent:@".kuri"];
}

- (void)notice:(NSString *)message {
    NSAlert *alert = [[[NSAlert alloc] init] autorelease];
    alert.messageText = message;
    [alert beginSheetModalForWindow:self.window completionHandler:nil];
}

- (void)showLogs:(id)sender {
    NSString *path = [[self logsPath] stringByAppendingPathComponent:@"access.log"];
    if (![[NSFileManager defaultManager] fileExistsAtPath:path]) {
        [self notice:@"No access log is available yet."];
        return;
    }
    NSTask *task = [[[NSTask alloc] init] autorelease];
    task.executableURL = [NSURL fileURLWithPath:@"/usr/bin/open"];
    task.arguments = @[@"-t", path];
    if (![task launchAndReturnError:NULL]) [self notice:@"Could not open the access log."];
}

- (void)openLogDirectory:(id)sender {
    if (![[NSWorkspace sharedWorkspace] openURL:[NSURL fileURLWithPath:[self logsPath] isDirectory:YES]])
        [self notice:@"Could not open the log directory."];
}

- (void)reset:(id)sender {
    NSAlert *alert = [[[NSAlert alloc] init] autorelease];
    alert.messageText = @"Reset kuri settings?";
    alert.informativeText = @"Restore the default settings and keep your bookmarks. This action can't be undone.";
    [alert addButtonWithTitle:@"Continue"];
    [alert addButtonWithTitle:@"Cancel"].keyEquivalent = @"\e";
    [alert beginSheetModalForWindow:self.window completionHandler:^(NSModalResponse result) {
        if (result == NSAlertFirstButtonReturn) [self submit:@{@"reset": @YES}];
    }];
}

- (void)dismiss:(BOOL)quit {
    [self.window orderOut:nil];
    if (quit) kuriGoAction(3);
}

- (void)confirmClose:(BOOL)quit {
    if (self.busy || self.confirmingClose || self.window.attachedSheet) return;
    if (self.dialog) {
        [self.dialog makeKeyAndOrderFront:nil];
        return;
    }
    NSMutableDictionary *draft = [[[self draft] mutableCopy] autorelease];
    if (self.keepAlive.state != NSControlStateValueOn) [draft removeObjectForKey:@"minutes"];
    if (self.autoHealing.state != NSControlStateValueOn) [draft removeObjectForKey:@"check_seconds"];
    if (!draft.count) {
        [self dismiss:quit];
        return;
    }
    self.confirmingClose = YES;
    NSAlert *alert = [[[NSAlert alloc] init] autorelease];
    alert.messageText = @"Save changes before closing?";
    alert.informativeText = @"You have unsaved changes.";
    [alert addButtonWithTitle:@"Save All"];
    [alert addButtonWithTitle:@"Discard Changes"];
    [alert addButtonWithTitle:@"Cancel"].keyEquivalent = @"\e";
    [alert beginSheetModalForWindow:self.window completionHandler:^(NSModalResponse result) {
        self.confirmingClose = NO;
        if (result == NSAlertFirstButtonReturn) {
            self.closeAfterSave = YES;
            self.quitAfterSave = quit;
            [self submit:draft];
        } else if (result == NSAlertSecondButtonReturn) {
            [self dismiss:quit];
        }
    }];
}

- (BOOL)windowShouldClose:(NSWindow *)sender {
    if (sender == self.dialog) {
        [self closeDialog:nil];
        return NO;
    }
    [self confirmClose:NO];
    return NO;
}

- (void)requestQuit {
    if (self.window.isVisible) [self confirmClose:YES];
    else kuriGoAction(3);
}
@end
