#import "settings_darwin.h"

@interface KuriSettings (PayloadFixture)
- (void)toggle:(NSButton *)sender;
- (void)changeContext:(NSSwitch *)sender;
- (void)saveServer:(id)sender;
- (void)saveForward:(id)sender;
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

int main(void) {
    @autoreleasepool {
        KuriSettings *settings = [[[KuriSettings alloc] initWithWindow:nil] autorelease];
        NSArray<NSString *> *styles = @[@"explicit_context", @"explicit_port", @"current_context"];
        [settings setValue:@{styles[0]: @YES, styles[1]: @YES, styles[2]: @YES} forKey:@"styles"];
        NSArray<NSString *> *checkboxes = [@[@"register", @"auto_start", @"keep_alive"] arrayByAddingObjectsFromArray:styles];
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
