#import "settings_darwin.h"
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
- (void)openTransferDialog:(BOOL)importing;
- (NSArray *)selectedTransferFields;
- (void)selectAllTransfer:(id)sender;
- (void)deselectAllTransfer:(id)sender;
- (void)closeDialog:(id)sender;
@end

@interface TransferFixtureSettings : KuriSettings
@end

@implementation TransferFixtureSettings
- (void)showDialog {}
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
        if (argc == 2 && strcmp(argv[1], "transfer") == 0) {
            [NSApplication sharedApplication];
            settings = [[[TransferFixtureSettings alloc] init] autorelease];
            [settings openTransferDialog:NO];
            if (settings.selectedTransferFields.count != 10) abort();
            [settings deselectAllTransfer:nil];
            if (settings.selectedTransferFields.count != 0 || [[settings valueForKey:@"transferConfirm"] isEnabled]) abort();
            [settings closeDialog:nil];
            [settings setValue:@{@"fields": @[@"context_aliases", @"port"]} forKey:@"transferPreview"];
            [settings openTransferDialog:YES];
            [settings deselectAllTransfer:nil];
            [settings selectAllTransfer:nil];
            if (![[NSSet setWithArray:settings.selectedTransferFields] isEqual:[NSSet setWithArray:@[@"port", @"context_aliases"]]]) abort();
            puts("ok");
            return 0;
        }
        if (argc == 2 && strcmp(argv[1], "tabs") == 0) {
            [NSApplication sharedApplication];
            settings = [[[KuriSettings alloc] init] autorelease];
            NSTabView *tabs = [settings valueForKey:@"tabs"];
            if (tabs.numberOfTabViewItems != 3 || !settings.window.toolbar) abort();
            NSMutableArray *titles = [NSMutableArray array];
            for (NSTabViewItem *tab in tabs.tabViewItems) {
                if (![tab.view isKindOfClass:NSScrollView.class]) abort();
                [titles addObject:tab.label];
            }
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
