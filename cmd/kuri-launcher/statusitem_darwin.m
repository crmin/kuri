//go:build cgo

#import <AppKit/AppKit.h>
#import "statusitem_darwin.h"
#import "popover_darwin.h"
#import "settings_darwin.h"
#import "shortcuts_darwin.h"

extern void kuriGoReady(void);
extern void kuriGoVisible(int visible);
extern void kuriGoAction(int action);

@interface KuriStatus : NSObject <NSApplicationDelegate, NSMenuDelegate>
@property(nonatomic, retain) NSStatusItem *item;
@property(nonatomic, retain) NSMenu *rightMenu;
@property(nonatomic, retain) KuriPopover *popover;
@property(nonatomic, retain) KuriSettings *settings;
@property(nonatomic, retain) NSMenuItem *serverItem;
@property(nonatomic, retain) NSMenuItem *errorItem;
@property(nonatomic, retain) NSDictionary *view;
- (void)applyView:(NSDictionary *)view;
@end

static KuriStatus *application;

@implementation KuriStatus
- (void)applicationDidFinishLaunching:(NSNotification *)notification {
    NSAssert([NSThread isMainThread], @"AppKit must run on the main thread.");
    self.item = [[NSStatusBar systemStatusBar] statusItemWithLength:NSSquareStatusItemLength];
    NSStatusBarButton *button = self.item.button;
    NSImage *image = [NSImage imageWithSystemSymbolName:@"k.circle" accessibilityDescription:@"Kuri"];
    if (!image) image = [NSImage imageNamed:NSImageNameActionTemplate];
    image.template = YES;
    button.image = image;
    button.toolTip = @"Kuri";
    [button setAccessibilityLabel:@"Kuri"];
    NSClickGestureRecognizer *right = [[[NSClickGestureRecognizer alloc] initWithTarget:self action:@selector(showRightMenu:)] autorelease];
    right.buttonMask = 0x2;
    [button addGestureRecognizer:right];
    button.target = self;
    button.action = @selector(showPopover:);
    self.popover = [[[KuriPopover alloc] init] autorelease];

    [self buildRightMenu];
    [self applyView:@{}];
    kuriGoReady();
}

- (void)buildRightMenu {
    self.rightMenu = [[[NSMenu alloc] initWithTitle:@"Kuri"] autorelease];
    self.rightMenu.delegate = self;
    self.rightMenu.autoenablesItems = NO;
    self.serverItem = [[[NSMenuItem alloc] initWithTitle:@"Checking..." action:@selector(changeServer:) keyEquivalent:@""] autorelease];
    self.serverItem.target = self;
    [self.rightMenu addItem:self.serverItem];
    self.errorItem = [[[NSMenuItem alloc] initWithTitle:@"" action:nil keyEquivalent:@""] autorelease];
    self.errorItem.enabled = NO;
    self.errorItem.hidden = YES;
    [self.rightMenu addItem:self.errorItem];
    [self.rightMenu addItem:[NSMenuItem separatorItem]];
    NSMenuItem *settings = [[[NSMenuItem alloc] initWithTitle:@"Settings..." action:@selector(openSettings:) keyEquivalent:@","] autorelease];
    settings.target = self;
    settings.keyEquivalentModifierMask = NSEventModifierFlagCommand;
    [self.rightMenu addItem:settings];
    [self.rightMenu addItem:[NSMenuItem separatorItem]];
    NSMenuItem *quit = [[[NSMenuItem alloc] initWithTitle:@"Quit" action:@selector(quit:) keyEquivalent:@"q"] autorelease];
    quit.target = self;
    quit.keyEquivalentModifierMask = NSEventModifierFlagCommand;
    [self.rightMenu addItem:quit];
}

- (void)applyView:(NSDictionary *)view {
    NSAssert([NSThread isMainThread], @"AppKit must run on the main thread.");
    self.view = view;
    [self.popover applyView:view];
    NSString *mode = view[@"mode"];
    BOOL known = [view[@"known"] boolValue];
    BOOL busy = [view[@"busy"] boolValue];
    BOOL quitting = [view[@"quitting"] boolValue];
    self.serverItem.title = quitting ? @"Quitting..." : busy ? @"Working..." : !known ? @"Unavailable" :
        [mode isEqualToString:@"stopped"] ? @"Start" : @"Stop";
    self.serverItem.enabled = known && !busy && !quitting && ![mode isEqualToString:@"foreground"];
    NSString *error = view[@"error"];
    if (!error.length && [mode isEqualToString:@"foreground"]) {
        error = @"Stop the server in its terminal.";
    }
    self.errorItem.hidden = error.length == 0;
    self.errorItem.title = error.length > 100 ? [[error substringToIndex:100] stringByAppendingString:@"..."] : error ?: @"";
    self.item.button.toolTip = error.length ? [@"Kuri: " stringByAppendingString:error] : @"Kuri";
}

- (void)showPopover:(id)sender { [self.popover toggleAt:self.item.button]; }

- (void)showRightMenu:(id)sender {
    [self.popover close];
    NSStatusBarButton *button = self.item.button;
    id monitor = [NSEvent addLocalMonitorForEventsMatchingMask:NSEventMaskKeyDown handler:^NSEvent *(NSEvent *event) {
        if (!kuriCommandKey(event, KuriKeyQ, @"q")) return event;
        [self.rightMenu cancelTracking];
        [self quit:nil];
        return nil;
    }];
    [self.rightMenu popUpMenuPositioningItem:nil atLocation:NSMakePoint(0, NSHeight(button.bounds)) inView:button];
    [NSEvent removeMonitor:monitor];
}

- (void)changeServer:(id)sender {
    kuriGoAction([self.view[@"mode"] isEqualToString:@"stopped"] ? 1 : 2);
}

- (void)openSettings:(id)sender { kuriOpenSettings(); }
- (void)quit:(id)sender { kuriRequestQuit(); }
- (void)menuWillOpen:(NSMenu *)menu { kuriGoVisible(1); }
- (void)menuDidClose:(NSMenu *)menu { kuriGoVisible(0); }
- (BOOL)menuHasKeyEquivalent:(NSMenu *)menu forEvent:(NSEvent *)event target:(id *)target action:(SEL *)action {
    if (kuriCommandKey(event, KuriKeyQ, @"q")) {
        *target = self;
        *action = @selector(quit:);
        return YES;
    }
    if (kuriCommandKey(event, 43, @",")) {
        *target = self;
        *action = @selector(openSettings:);
        return YES;
    }
    return NO;
}
- (NSApplicationTerminateReply)applicationShouldTerminate:(NSApplication *)sender {
    kuriRequestQuit();
    return NSTerminateCancel;
}
@end

void kuriOpenSettings(void) {
    [application.popover close];
    if (!application.settings) application.settings = [[[KuriSettings alloc] init] autorelease];
    [application.settings open];
}

void kuriRequestQuit(void) {
    if (application.settings) [application.settings requestQuit];
    else kuriGoAction(3);
}

void kuriRenderSettings(const char *json) {
    @autoreleasepool {
        NSString *text = [[NSString alloc] initWithUTF8String:json];
        dispatch_async(dispatch_get_main_queue(), ^{
            NSDictionary *view = [NSJSONSerialization JSONObjectWithData:[text dataUsingEncoding:NSUTF8StringEncoding]
                options:0 error:NULL];
            if ([view isKindOfClass:[NSDictionary class]]) [application.settings applyResponse:view];
        });
        [text release];
    }
}

void kuriRun(void) {
    @autoreleasepool {
        [NSApplication sharedApplication];
        [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
        application = [[KuriStatus alloc] init];
        NSApp.delegate = application;
        [NSApp run];
    }
}

void kuriRender(const char *json) {
    @autoreleasepool {
        NSString *text = [[NSString alloc] initWithUTF8String:json];
        CFRunLoopRef loop = CFRunLoopGetMain();
        NSArray *modes = @[NSDefaultRunLoopMode, NSEventTrackingRunLoopMode];
        CFRunLoopPerformBlock(loop, (CFArrayRef)modes, ^{
            NSDictionary *view = [NSJSONSerialization JSONObjectWithData:[text dataUsingEncoding:NSUTF8StringEncoding] options:0 error:NULL];
            if ([view isKindOfClass:[NSDictionary class]]) [application applyView:view];
        });
        CFRunLoopWakeUp(loop);
        [text release];
    }
}

void kuriTerminate(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        [application.popover close];
        [[NSStatusBar systemStatusBar] removeStatusItem:application.item];
        NSApp.delegate = nil;
        [NSApp terminate:nil];
    });
}
