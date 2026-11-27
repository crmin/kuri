//go:build cgo

#import "settings_darwin.h"

extern void kuriGoSettingsRequest(char *edit);
extern void kuriGoAction(int action);

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

@interface KuriSettings ()
@property(nonatomic, retain) NSStackView *body;
@property(nonatomic, retain) NSStackView *contexts;
@property(nonatomic, retain) NSTextField *path;
@property(nonatomic, retain) NSTextField *problem;
@property(nonatomic, retain) NSTextField *serverError;
@property(nonatomic, retain) NSTextField *contextError;
@property(nonatomic, retain) NSTextField *forwardError;
@property(nonatomic, retain) NSButton *registration;
@property(nonatomic, retain) NSButton *autoStart;
@property(nonatomic, retain) NSButton *keepAlive;
@property(nonatomic, retain) NSButton *retry;
@property(nonatomic, retain) NSPopUpButton *trigger;
@property(nonatomic, retain) NSMutableArray<NSControl *> *controls;
@property(nonatomic, retain) NSMutableDictionary<NSString *, NSTextField *> *fields;
@property(nonatomic, retain) NSMutableDictionary<NSString *, NSButton *> *styles;
@property(nonatomic, retain) NSDictionary *saved;
@property(nonatomic, retain) NSDictionary *pending;
@property(nonatomic, retain) NSString *settingsPath;
@property(nonatomic) BOOL busy;
@property(nonatomic) BOOL loaded;
@property(nonatomic) BOOL corrupt;
@property(nonatomic) BOOL registrationAvailable;
@property(nonatomic) BOOL closeAfterSave;
@property(nonatomic) BOOL quitAfterSave;
@property(nonatomic) BOOL confirmingClose;
@property(nonatomic) BOOL portDraftUnapplied;
@end

@implementation KuriSettings
- (instancetype)init {
    NSWindow *window = [[[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, 650, 660)
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
    }
    [self.window makeKeyAndOrderFront:nil];
}

- (void)addSection:(NSString *)name {
    NSTextField *heading = [NSTextField labelWithString:name];
    heading.font = [NSFont boldSystemFontOfSize:14];
    [self.body addArrangedSubview:heading];
}

- (void)addDescription:(NSString *)text {
    addFullWidth(self.body, settingsLabel(text, NSColor.secondaryLabelColor));
}

- (NSButton *)button:(NSString *)title action:(SEL)action {
    NSButton *button = [NSButton buttonWithTitle:title target:self action:action];
    [self.controls addObject:button];
    return button;
}

- (NSButton *)checkbox:(NSString *)title identifier:(NSString *)identifier {
    NSButton *button = [NSButton checkboxWithTitle:title target:self action:@selector(toggle:)];
    button.identifier = identifier;
    [self.controls addObject:button];
    [self.body addArrangedSubview:button];
    return button;
}

- (void)field:(NSString *)name identifier:(NSString *)identifier width:(CGFloat)width unit:(NSString *)unit {
    NSStackView *row = settingsStack(NSUserInterfaceLayoutOrientationHorizontal, 9);
    NSTextField *label = [NSTextField labelWithString:name];
    NSTextField *field = [[[NSTextField alloc] initWithFrame:NSZeroRect] autorelease];
    [field setAccessibilityLabel:name];
    field.identifier = identifier;
    field.maximumNumberOfLines = 1;
    [row addArrangedSubview:label];
    [label.widthAnchor constraintEqualToConstant:166].active = YES;
    [row addArrangedSubview:field];
    if (width > 0) [field.widthAnchor constraintEqualToConstant:width].active = YES;
    if (unit) [row addArrangedSubview:[NSTextField labelWithString:unit]];
    if (width > 0) [self.body addArrangedSubview:row];
    else addFullWidth(self.body, row);
    [self.controls addObject:field];
    self.fields[identifier] = field;
}

- (void)build {
    NSStackView *body = nil;
    NSScrollView *scroll = settingsScroll(&body, 24, 9, NSNoBorder);
    self.body = body;
    [self.window setContentView:scroll];
    self.problem = settingsLabel(@"", NSColor.systemRedColor);
    self.problem.hidden = YES;
    addFullWidth(body, self.problem);
    self.retry = [self button:@"Retry Applying Settings" action:@selector(retry:)];
    self.retry.hidden = YES;
    [body addArrangedSubview:self.retry];

    [self addSection:@"General"];
    self.registration = [self checkbox:@"Register kuri at system startup" identifier:@"register"];
    self.autoStart = [self checkbox:@"Start the reverse proxy automatically" identifier:@"auto_start"];
    [self addDescription:@"Starts the proxy when you open kuri, at system startup if registered, or when a connection reaches a registered proxy. Turn this off to start the proxy manually."];

    [self addSection:@"Kubernetes"];
    NSStackView *pathRow = settingsStack(NSUserInterfaceLayoutOrientationHorizontal, 9);
    [pathRow addArrangedSubview:[NSTextField labelWithString:@"Kubeconfig Path"]];
    self.path = [NSTextField labelWithString:@""];
    self.path.font = [NSFont monospacedSystemFontOfSize:12 weight:NSFontWeightRegular];
    self.path.lineBreakMode = NSLineBreakByTruncatingMiddle;
    [self.path setContentCompressionResistancePriority:NSLayoutPriorityDefaultLow forOrientation:NSLayoutConstraintOrientationHorizontal];
    [pathRow addArrangedSubview:self.path];
    [pathRow addArrangedSubview:[self button:@"Open..." action:@selector(openConfig:)]];
    addFullWidth(body, pathRow);
    [body addArrangedSubview:[NSTextField labelWithString:@"Contexts"]];
    [self addDescription:@"The reverse proxy routes requests only to the Kubernetes contexts you enable here."];
    self.contextError = settingsLabel(@"", NSColor.systemRedColor);
    self.contextError.hidden = YES;
    addFullWidth(body, self.contextError);
    NSStackView *contexts = nil;
    NSScrollView *contextScroll = settingsScroll(&contexts, 8, 3, NSBezelBorder);
    self.contexts = contexts;
    [contextScroll.heightAnchor constraintEqualToConstant:145].active = YES;
    addFullWidth(body, contextScroll);

    [self addSection:@"Server"];
    [body addArrangedSubview:[NSTextField labelWithString:@"Host"]];
    [self addDescription:@"If another service uses port 80, choose a different port. To keep using port 80, configure that service to forward requests to kuri's new port."];
    [self field:@"Service Domain" identifier:@"domain" width:0 unit:nil];
    [self field:@"Port" identifier:@"port" width:85 unit:nil];
    self.serverError = settingsLabel(@"", NSColor.systemRedColor);
    self.serverError.hidden = YES;
    addFullWidth(body, self.serverError);
    [body addArrangedSubview:[self button:@"Save Server Settings" action:@selector(saveServer:)]];
    [body addArrangedSubview:[NSTextField labelWithString:@"Domain Styles"]];
    for (NSString *style in @[@"explicit_context", @"explicit_port", @"current_context"]) {
        NSButton *button = [self checkbox:@"" identifier:style];
        self.styles[style] = button;
    }

    [self addSection:@"Port Forwarding"];
    [body addArrangedSubview:[NSTextField labelWithString:@"Keep-Alive"]];
    [self addDescription:@"Keep the port forward open after the first request so later requests can connect faster."];
    self.keepAlive = [self checkbox:@"Enable port forwarding keep-alive" identifier:@"keep_alive"];
    [self field:@"Keep-alive duration" identifier:@"minutes" width:85 unit:@"minutes"];
    NSStackView *timer = settingsStack(NSUserInterfaceLayoutOrientationHorizontal, 9);
    [timer addArrangedSubview:[NSTextField labelWithString:@"Start the keep-alive timer from"]];
    self.trigger = [[[NSPopUpButton alloc] initWithFrame:NSZeroRect pullsDown:NO] autorelease];
    [self.trigger addItemsWithTitles:@[@"Last request", @"When the port forward opens"]];
    self.trigger.target = self;
    self.trigger.action = @selector(changeTrigger:);
    [self.trigger setAccessibilityLabel:@"Start the keep-alive timer from"];
    [self.controls addObject:self.trigger];
    [timer addArrangedSubview:self.trigger];
    [body addArrangedSubview:timer];
    [body addArrangedSubview:[NSTextField labelWithString:@"Port Range"]];
    [self addDescription:@"kuri uses ports in this range for port forwarding. If no port is available, kuri closes its oldest open port forward to make room for a new one, even when keep-alive is enabled."];
    [self field:@"Min" identifier:@"min_port" width:85 unit:nil];
    [self field:@"Max" identifier:@"max_port" width:85 unit:nil];
    self.forwardError = settingsLabel(@"", NSColor.systemRedColor);
    self.forwardError.hidden = YES;
    addFullWidth(body, self.forwardError);
    [body addArrangedSubview:[self button:@"Save Port Forwarding Settings" action:@selector(saveForward:)]];
    [self addSection:@"Logs"];
    NSStackView *logs = settingsStack(NSUserInterfaceLayoutOrientationHorizontal, 8);
    [logs addArrangedSubview:[NSButton buttonWithTitle:@"Show Logs" target:self action:@selector(showLogs:)]];
    [logs addArrangedSubview:[NSButton buttonWithTitle:@"Open Log Directory" target:self action:@selector(openLogDirectory:)]];
    [body addArrangedSubview:logs];
    [self addSection:@"Reset"];
    [body addArrangedSubview:[self button:@"Reset Settings..." action:@selector(reset:)]];
    [self updateControls];
}

- (void)submit:(NSDictionary *)edit {
    if (self.busy) return;
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
        control.enabled = !self.busy && self.loaded && (!self.corrupt || reset);
    }
    self.registration.enabled = self.registration.enabled && self.registrationAvailable;
    self.fields[@"minutes"].enabled = self.fields[@"minutes"].enabled && self.keepAlive.state == NSControlStateValueOn;
    self.trigger.enabled = self.trigger.enabled && self.keepAlive.state == NSControlStateValueOn;
    for (NSStackView *row in self.contexts.arrangedSubviews) {
        NSSwitch *toggle = (NSSwitch *)row.arrangedSubviews.lastObject;
        toggle.enabled = self.loaded && !self.busy && !self.corrupt;
    }
}

- (NSDictionary *)draft {
    NSMutableDictionary *draft = [NSMutableDictionary dictionary];
    for (NSString *key in self.fields) {
        NSTextField *field = self.fields[key];
        if (field == self.fields[@"minutes"] && self.keepAlive.state != NSControlStateValueOn) continue;
        NSString *text = field.currentEditor ? field.currentEditor.string : field.stringValue;
        if (![text isEqualToString:self.saved[key] ?: @""] || ([key isEqual:@"port"] && self.portDraftUnapplied))
            draft[key] = text;
    }
    return draft;
}

- (void)applyResponse:(NSDictionary *)response {
    NSDictionary *pending = self.pending;
    NSDictionary *draft = [self draft];
    BOOL saved = [response[@"saved"] boolValue];
    BOOL applied = [response[@"applied"] boolValue];
    NSString *portError = response[@"port_error"];
    NSString *error = response[@"error"];
    BOOL hostEdit = pending[@"domain"] || pending[@"port"];
    BOOL forwardEdit = pending[@"minutes"] || pending[@"min_port"] || pending[@"max_port"];
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
        self.registrationAvailable = [response[@"registration_error"] length] == 0;
        NSDictionary *general = cfg[@"general"], *kube = cfg[@"kubernetes"];
        NSDictionary *server = cfg[@"server"], *forward = cfg[@"port_forward"];
        self.registration.state = [general[@"launch_at_login"] boolValue];
        self.autoStart.state = [general[@"auto_start_proxy"] boolValue];
        self.keepAlive.state = [forward[@"keep_alive_enabled"] boolValue];
        [self.trigger selectItemAtIndex:[forward[@"keep_alive_trigger"] isEqual:@"forward_opened"] ? 1 : 0];
        self.saved = @{
            @"domain": settingsString(server[@"domain"]), @"port": settingsString(server[@"port"]),
            @"minutes": settingsString(forward[@"keep_alive_minutes"]),
            @"min_port": settingsString(forward[@"min_port"]), @"max_port": settingsString(forward[@"max_port"]),
        };
        for (NSString *key in self.fields) {
            BOOL restoredPort = [key isEqual:@"port"] && portError.length != 0;
            BOOL acceptSaved = (saved && pending[key] && !restoredPort) || (reset && applied) ||
                ([key isEqual:@"minutes"] && self.keepAlive.state == NSControlStateValueOff);
            NSString *text = restoredPort && pending[key] ? pending[key] :
                (!pending || acceptSaved) ? self.saved[key] : draft[key] ?: self.saved[key];
            self.fields[key].stringValue = text ?: @"";
        }

        self.settingsPath = settingsString(kube[@"config_path"]);
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
        self.retry.hidden = ![response[@"apply_pending"] boolValue] || portError.length || ![notice isEqual:response[@"runtime_warning"]];
        self.problem.toolTip = notice;
    }
    self.problem.stringValue = notice ?: @"";
    self.problem.hidden = !notice.length;

    self.busy = NO;
    self.pending = nil;
    [self updateControls];
    if (portError.length) [self reveal:self.serverError];
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
        NSTextField *title = settingsLabel(name, NSColor.labelColor);
        title.lineBreakMode = NSLineBreakByCharWrapping;
        [row addArrangedSubview:title];
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

- (void)renderStyles:(NSDictionary *)server {
    NSDictionary *templates = @{
        @"explicit_context": @"service.namespace.ctx",
        @"explicit_port": @"port.service.namespace.ctx",
        @"current_context": @"service.namespace"
    };
    NSNumber *port = server[@"port"];
    NSString *suffix = port.integerValue == 80 ? [@"." stringByAppendingString:settingsString(server[@"domain"])] :
        [NSString stringWithFormat:@".%@:%@", settingsString(server[@"domain"]), settingsString(port)];
    for (NSString *key in self.styles) {
        NSButton *button = self.styles[key];
        NSString *label = [templates[key] stringByAppendingString:suffix];
        NSMutableAttributedString *title = [[[NSMutableAttributedString alloc] initWithString:label attributes:
            @{NSForegroundColorAttributeName: NSColor.labelColor, NSFontAttributeName: button.font}] autorelease];
        [title addAttribute:NSForegroundColorAttributeName value:NSColor.secondaryLabelColor
            range:NSMakeRange(0, [templates[key] length])];
        button.attributedTitle = title;
        [button setAccessibilityLabel:label];
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
    alert.messageText = @"Reset all settings?";
    alert.informativeText = @"This will restore all saved settings to their defaults. This action can't be undone.";
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
    NSDictionary *draft = [self draft];
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
    [self confirmClose:NO];
    return NO;
}

- (void)requestQuit {
    if (self.window.isVisible) [self confirmClose:YES];
    else kuriGoAction(3);
}
@end
