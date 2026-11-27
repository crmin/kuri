#import "settings_darwin.h"
#import "popover_darwin.h"
#import "shortcuts_darwin.h"
#import <ServiceManagement/ServiceManagement.h>
#import <objc/runtime.h>

#include <string.h>

@interface KuriSettings (PayloadFixture)
- (void)toggle:(NSButton *)sender;
- (void)addLoginItemToSection:(NSStackView *)section;
- (void)changeContext:(NSSwitch *)sender;
- (void)saveServer:(id)sender;
- (void)saveForward:(id)sender;
- (void)renderStyles:(NSDictionary *)server;
- (void)openTransferDialog:(BOOL)importing keepingSelection:(NSArray *)selection;
- (void)updateControls;
- (NSArray *)selectedTransferFields;
- (void)selectAllTransfer:(id)sender;
- (void)deselectAllTransfer:(id)sender;
- (void)closeDialog:(id)sender;
- (void)openAlias:(NSButton *)sender;
- (void)saveAlias:(id)sender;
- (void)selectTab:(NSString *)identifier;
@end

@interface TransferFixtureSettings : KuriSettings
@end

@implementation TransferFixtureSettings
- (void)showDialog { [self updateControls]; }
@end

@interface KuriSettingsWindow : NSWindow
@end

@interface ShortcutFixtureWindow : KuriSettingsWindow
@property(nonatomic) BOOL closePressed;
@end

@implementation ShortcutFixtureWindow
- (BOOL)isKeyWindow { return YES; }
- (void)performClose:(id)sender { self.closePressed = YES; }
@end

@interface KuriStatus : NSObject <NSMenuDelegate>
- (void)buildRightMenu;
@property(nonatomic, readonly) NSMenu *rightMenu;
@end

@interface FixtureControl : NSObject
@property(nonatomic, copy) NSString *identifier;
@property(nonatomic) NSControlStateValue state;
@property(nonatomic, copy) NSString *stringValue;
@property(nonatomic, getter=isEnabled) BOOL enabled;
@end

@implementation FixtureControl
- (void)dealloc {
    [_identifier release];
    [_stringValue release];
    [super dealloc];
}
@end

static NSString *caseName;
static BOOL openedLoginItems;

static NSEvent *keyEvent(NSString *characters, unsigned short key, NSEventModifierFlags modifiers) {
    return [NSEvent keyEventWithType:NSEventTypeKeyDown location:NSZeroPoint modifierFlags:modifiers
        timestamp:0 windowNumber:0 context:nil characters:characters charactersIgnoringModifiers:characters
        isARepeat:NO keyCode:key];
}

static void captureLoginItemsOpen(id receiver, SEL selector) {
    openedLoginItems = YES;
}

void kuriGoSettingsRequest(char *edit) {
    if (!caseName || !edit) abort();
    printf("%s\t%s\n", caseName.UTF8String, edit);
}

void kuriGoAction(int action) { abort(); }
void kuriGoReady(void) { abort(); }
void kuriGoVisible(int visible) { abort(); }

static FixtureControl *text(NSString *value) {
    FixtureControl *control = [[[FixtureControl alloc] init] autorelease];
    control.stringValue = value;
    return control;
}

static void renderStyleLabels(KuriSettings *settings) {
    [NSApplication sharedApplication];
    NSButton *style = [[[NSButton alloc] initWithFrame:NSZeroRect] autorelease];
    style.font = [NSFont systemFontOfSize:13];
    [settings setValue:@{@"explicit_port": style} forKey:@"styles"];
    for (NSDictionary *server in @[
        @{@"domain": @"kuri.localhost", @"port": @80, @"domain_styles": @{@"explicit_port": @YES}},
        @{@"domain": @"example.test", @"port": @8081, @"domain_styles": @{@"explicit_port": @NO}}
    ]) {
        [settings renderStyles:server];
        printf("%s\t%s\t%s\t%ld\n", style.attributedTitle.string.UTF8String,
               style.toolTip.UTF8String, style.accessibilityLabel.UTF8String, (long)style.state);
    }
}

int main(int argc, char **argv) {
    @autoreleasepool {
        KuriSettings *settings = [[[KuriSettings alloc] initWithWindow:nil] autorelease];
        if (argc == 2 && strcmp(argv[1], "alias-dialog") == 0) {
            [NSApplication sharedApplication];
            settings = [[[TransferFixtureSettings alloc] init] autorelease];
            [settings applyResponse:@{@"kubeconfig_fingerprint": @"fingerprint", @"contexts": @[@"aws-dev"],
                @"config": @{@"kubernetes": @{@"config_path": @"/tmp/kubeconfig",
                    @"context_aliases": @{@"/tmp/kubeconfig": @{@"aws-dev": @"previous"}}}}}];
            NSStackView *contexts = [settings valueForKey:@"contexts"];
            NSStackView *row = contexts.arrangedSubviews.firstObject;
            NSStackView *labels = row.arrangedSubviews.firstObject;
            NSTextField *displayAlias = labels.arrangedSubviews.lastObject;
            if (![displayAlias.stringValue isEqualToString:@"(previous)"] || ![displayAlias.textColor isEqual:NSColor.secondaryLabelColor]) abort();
            NSButton *edit = row.arrangedSubviews[1];
            [settings openAlias:edit];
            NSWindow *dialog = [settings valueForKey:@"dialog"];
            NSTextField *input = [settings valueForKey:@"aliasField"];
            if (![dialog.title isEqualToString:@"aws-dev"] || ![input.stringValue isEqualToString:@"aws-dev"]) abort();
            [settings closeDialog:nil];
            [settings openAlias:edit];
            input = [settings valueForKey:@"aliasField"];
            input.stringValue = @"dev";
            caseName = @"save_alias";
            [settings saveAlias:nil];
            return 0;
        }
        if (argc == 2 && strcmp(argv[1], "auto-healing") == 0) {
            [NSApplication sharedApplication];
            settings = [[[TransferFixtureSettings alloc] init] autorelease];
            [settings applyResponse:@{@"config": @{@"port_forward": @{}}}];
            NSButton *switchControl = [settings valueForKey:@"autoHealing"];
            NSTextField *interval = [[settings valueForKey:@"fields"] objectForKey:@"check_seconds"];
            if (switchControl.state != NSControlStateValueOn || !interval.enabled || ![interval.stringValue isEqualToString:@"10"]) abort();
            [settings applyResponse:@{@"config": @{@"port_forward": @{@"auto_healing_enabled": @NO, @"connection_check_interval_seconds": @20}}}];
            if (switchControl.state != NSControlStateValueOff || interval.enabled || ![interval.stringValue isEqualToString:@"20"]) abort();
            puts("ok");
            return 0;
        }
        if (argc == 2 && strcmp(argv[1], "transfer") == 0) {
            [NSApplication sharedApplication];
            settings = [[[TransferFixtureSettings alloc] init] autorelease];
            [settings openTransferDialog:NO keepingSelection:nil];
            if (settings.selectedTransferFields.count != 10) abort();
            [settings deselectAllTransfer:nil];
            if (settings.selectedTransferFields.count != 0 || [[settings valueForKey:@"transferConfirm"] isEnabled]) abort();
            [settings closeDialog:nil];
            [settings setValue:@{@"fields": @[@"context_aliases", @"port"]} forKey:@"transferPreview"];
            [settings openTransferDialog:YES keepingSelection:nil];
            [settings deselectAllTransfer:nil];
            [settings selectAllTransfer:nil];
            if (![[NSSet setWithArray:settings.selectedTransferFields] isEqual:[NSSet setWithArray:@[@"port", @"context_aliases"]]]) abort();
            puts("ok");
            return 0;
        }
        if (argc == 2 && strcmp(argv[1], "transfer-refresh") == 0) {
            [NSApplication sharedApplication];
            settings = [[[TransferFixtureSettings alloc] init] autorelease];
            [settings setValue:@"/tmp/import.yaml" forKey:@"transferPath"];
            [settings setValue:@{@"transfer_operation": @"import", @"fields": @[@"context_aliases"]} forKey:@"pending"];
            caseName = @"refresh";
            [settings applyResponse:@{@"refresh": @YES}];
            NSDictionary *request = [settings valueForKey:@"pending"];
            if (![request[@"fields"] isEqual:@[@"context_aliases"]]) abort();
            [settings applyResponse:@{@"fields": @[@"domain", @"port", @"context_aliases"]}];
            if (![settings.selectedTransferFields isEqual:@[@"context_aliases"]]) abort();
            [settings closeDialog:nil];
            [settings setValue:request forKey:@"pending"];
            [settings applyResponse:@{@"fields": @[@"domain", @"port"]}];
            if (settings.selectedTransferFields.count || [[settings valueForKey:@"transferConfirm"] isEnabled]) abort();
            return 0;
        }
        if (argc == 2 && strcmp(argv[1], "transfer-draft") == 0) {
            [NSApplication sharedApplication];
            settings = [[[TransferFixtureSettings alloc] init] autorelease];
            NSDictionary *enabled = @{@"saved": @YES, @"config": @{@"port_forward": @{@"keep_alive_enabled": @YES, @"keep_alive_minutes": @60}}};
            NSDictionary *disabled = @{@"saved": @YES, @"config": @{@"port_forward": @{@"keep_alive_enabled": @NO, @"keep_alive_minutes": @60}}};
            [settings applyResponse:enabled];
            NSTextField *minutes = [[settings valueForKey:@"fields"] objectForKey:@"minutes"];
            minutes.stringValue = @"95";
            [settings setValue:@{@"transfer_operation": @"import", @"fields": @[@"keep_alive_enabled"]} forKey:@"pending"];
            [settings applyResponse:@{@"settings": disabled}];
            if (minutes.enabled || ![minutes.stringValue isEqualToString:@"95"]) abort();
            [settings setValue:@{@"auto_start": @YES} forKey:@"pending"];
            [settings applyResponse:disabled];
            if (![minutes.stringValue isEqualToString:@"95"]) abort();
            [settings setValue:@{@"keep_alive": @YES} forKey:@"pending"];
            [settings applyResponse:enabled];
            if (!minutes.enabled || ![minutes.stringValue isEqualToString:@"95"]) abort();
            [settings setValue:@{@"keep_alive": @NO} forKey:@"pending"];
            [settings applyResponse:disabled];
            if (![minutes.stringValue isEqualToString:@"60"]) abort();
            puts("ok");
            return 0;
        }
        if (argc == 2 && strcmp(argv[1], "tabs") == 0) {
            [NSApplication sharedApplication];
            settings = [[[KuriSettings alloc] init] autorelease];
            NSTabView *tabs = [settings valueForKey:@"tabs"];
            if (tabs.numberOfTabViewItems != 3 || !settings.window.toolbar) abort();
            for (NSAppearanceName appearance in @[NSAppearanceNameAqua, NSAppearanceNameDarkAqua]) {
                settings.window.appearance = [NSAppearance appearanceNamed:appearance];
                [settings.window setContentSize:NSMakeSize(490, 410)];
                [settings.window.contentView layoutSubtreeIfNeeded];
                if (NSWidth(tabs.frame) < 460 || NSHeight(tabs.frame) < 300) abort();
            }
            NSMutableArray *titles = [NSMutableArray array];
            for (NSTabViewItem *tab in tabs.tabViewItems) {
                if (![tab.view isKindOfClass:NSScrollView.class]) abort();
                [titles addObject:tab.label];
            }
            NSTextField *port = [[settings valueForKey:@"fields"] objectForKey:@"port"];
            [settings selectTab:@"Network"];
            port.stringValue = @"38473";
            [settings selectTab:@"About"];
            [settings selectTab:@"Network"];
            if (![port.stringValue isEqualToString:@"38473"] || ![settings.window.toolbar.selectedItemIdentifier isEqualToString:@"Network"]) abort();
            puts([titles componentsJoinedByString:@" "].UTF8String);
            return 0;
        }
        if (argc == 2 && strcmp(argv[1], "shortcuts") == 0) {
            [NSApplication sharedApplication];
            if (!kuriCommandKey(keyEvent(@"\u3148", 13, NSEventModifierFlagCommand), 13, @"w")) abort();
            if (!kuriCommandKey(keyEvent(@"\u3142", 12, NSEventModifierFlagCommand), 12, @"q")) abort();
            if (!kuriCommandKey(keyEvent(@"w", 8, NSEventModifierFlagCommand), 13, @"w")) abort();
            if (kuriCommandKey(keyEvent(@"w", 13, NSEventModifierFlagCommand | NSEventModifierFlagShift), 13, @"w")) abort();
            if (kuriCommandKey(keyEvent(@"w", 13, 0), 13, @"w")) abort();
            if (kuriCommandKey(keyEvent(@"z", 13, NSEventModifierFlagCommand), 13, @"w")) abort();
            if (kuriCommandKey(keyEvent(@",", 13, NSEventModifierFlagCommand), 13, @"w")) abort();
            ShortcutFixtureWindow *window = [[[ShortcutFixtureWindow alloc] initWithContentRect:NSMakeRect(0, 0, 120, 80)
                styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable backing:NSBackingStoreBuffered defer:NO] autorelease];
            for (NSString *characters in @[@"w", @"\u3148", @"\u0446"]) {
                window.closePressed = NO;
                if (![window performKeyEquivalent:keyEvent(characters, 13, NSEventModifierFlagCommand)] || !window.closePressed) abort();
            }
            puts("ok");
            return 0;
        }
        if (argc == 2 && strcmp(argv[1], "menus") == 0) {
            [NSApplication sharedApplication];
            KuriStatus *status = [[[KuriStatus alloc] init] autorelease];
            [status buildRightMenu];
            NSArray<NSMenuItem *> *items = status.rightMenu.itemArray;
            if (items.count < 3 || !items[items.count - 2].isSeparatorItem || ![items.lastObject.title isEqualToString:@"Quit"] ||
                ![items[items.count - 3].title isEqualToString:@"Settings..."]) abort();
            for (NSString *characters in @[@"q", @"\u3142", @"\u0439"]) {
                id target = nil;
                SEL action = NULL;
                if (![status menuHasKeyEquivalent:status.rightMenu forEvent:keyEvent(characters, 12, NSEventModifierFlagCommand)
                    target:&target action:&action] || target != status || action != @selector(quit:)) abort();
            }
            id target = nil;
            SEL action = NULL;
            if ([status menuHasKeyEquivalent:status.rightMenu forEvent:keyEvent(@"a", 12, NSEventModifierFlagCommand)
                target:&target action:&action]) abort();
            KuriPopover *popover = [[[KuriPopover alloc] init] autorelease];
            [popover applyView:@{@"known": @YES, @"mode": @"stopped"}];
            NSView *settingsRow = [popover valueForKey:@"settingsButton"], *quitRow = [popover valueForKey:@"quitButton"];
            NSView *separator = [popover valueForKey:@"quitSeparator"];
            if (NSMaxY(settingsRow.frame) > NSMinY(separator.frame) || NSMaxY(separator.frame) > NSMinY(quitRow.frame)) abort();
            puts("ok");
            return 0;
        }
        if (argc == 2 && strcmp(argv[1], "style-labels") == 0) {
            renderStyleLabels(settings);
            return 0;
        }
        if (argc == 2 && strcmp(argv[1], "login-item") == 0) {
            [NSApplication sharedApplication];
            NSStackView *section = [[[NSStackView alloc] initWithFrame:NSZeroRect] autorelease];
            [settings addLoginItemToSection:section];
            if (section.arrangedSubviews.count != 2) abort();
            NSStackView *row = section.arrangedSubviews[0];
            if (row.orientation != NSUserInterfaceLayoutOrientationHorizontal || row.arrangedSubviews.count != 2) abort();
            NSTextField *label = row.arrangedSubviews[0];
            NSButton *button = row.arrangedSubviews[1];
            NSTextField *description = section.arrangedSubviews[1];
            Method method = class_getClassMethod(SMAppService.class, @selector(openSystemSettingsLoginItems));
            IMP original = method_setImplementation(method, (IMP)captureLoginItemsOpen);
            [button performClick:nil];
            method_setImplementation(method, original);
            if (!openedLoginItems) abort();
            printf("%s\n%s\n%s\n%s\n", label.stringValue.UTF8String, button.title.UTF8String,
                   description.stringValue.UTF8String, sel_getName(button.action));
            return 0;
        }
        NSArray<NSString *> *styles = @[@"explicit_context", @"explicit_port", @"current_context"];
        [settings setValue:@{styles[0]: @YES, styles[1]: @YES, styles[2]: @YES} forKey:@"styles"];
        NSArray<NSString *> *checkboxes = [@[@"auto_start", @"keep_alive"] arrayByAddingObjectsFromArray:styles];
        FixtureControl *control = [[[FixtureControl alloc] init] autorelease];
        for (NSNumber *state in @[@(NSControlStateValueOff), @(NSControlStateValueOn)]) {
            control.state = state.integerValue;
            NSString *stateName = control.state == NSControlStateValueOn ? @"true" : @"false";
            for (NSString *identifier in checkboxes) {
                control.identifier = identifier;
                caseName = [NSString stringWithFormat:@"%@/%@", identifier, stateName];
                [settings toggle:(NSButton *)control];
                [settings setValue:@NO forKey:@"busy"];
            }
            control.identifier = @"dev";
            caseName = [@"context/" stringByAppendingString:stateName];
            [settings changeContext:(NSSwitch *)control];
            [settings setValue:@NO forKey:@"busy"];
        }

        [settings setValue:@{@"domain": text(@"kuri.localhost"), @"port": text(@"80"),
                            @"min_port": text(@"30000"), @"max_port": text(@"39999"), @"minutes": text(@"60")}
                    forKey:@"fields"];
        [settings setValue:@{@"port": @"80"} forKey:@"saved"];
        [settings setValue:control forKey:@"keepAlive"];
        caseName = @"save_server";
        [settings saveServer:nil];
        [settings setValue:@NO forKey:@"busy"];
        control.state = NSControlStateValueOff;
        caseName = @"save_forward_without_keep_alive";
        [settings saveForward:nil];
        [settings setValue:@NO forKey:@"busy"];
        control.state = NSControlStateValueOn;
        caseName = @"save_forward_with_keep_alive";
        [settings saveForward:nil];
    }
}
