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

void kuriGoSettingsRequest(char *edit) {
    if (!edit) abort();
    puts(edit);
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
        for (NSNumber *state in @[@(NSControlStateValueOn), @(NSControlStateValueOff)]) {
            control.state = state.integerValue;
            for (NSString *identifier in checkboxes) {
                control.identifier = identifier;
                [settings toggle:(NSButton *)control];
                [settings setValue:@NO forKey:@"busy"];
            }
            control.identifier = @"dev";
            [settings changeContext:(NSSwitch *)control];
            [settings setValue:@NO forKey:@"busy"];
        }

        [settings setValue:@{@"domain": text(@"kuri.localhost"), @"port": text(@"80"),
                            @"min_port": text(@"30000"), @"max_port": text(@"39999"), @"minutes": text(@"60")}
                    forKey:@"fields"];
        [settings setValue:@{@"port": @"80"} forKey:@"saved"];
        [settings setValue:control forKey:@"keepAlive"];
        [settings saveServer:nil];
        [settings setValue:@NO forKey:@"busy"];
        [settings saveForward:nil];
        [settings setValue:@NO forKey:@"busy"];
        control.state = NSControlStateValueOn;
        [settings saveForward:nil];
    }
}
