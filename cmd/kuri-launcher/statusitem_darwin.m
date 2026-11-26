//go:build cgo

#import <AppKit/AppKit.h>
#import "statusitem_darwin.h"

extern void kuriGoReady(void);
extern void kuriGoVisible(int visible);
extern void kuriGoAction(int action);

@interface KuriStatus : NSObject <NSApplicationDelegate, NSMenuDelegate>
@property(nonatomic, retain) NSStatusItem *item;
@property(nonatomic, retain) NSMenu *rightMenu;
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
    NSMenuItem *quit = [[[NSMenuItem alloc] initWithTitle:@"Quit" action:@selector(quit:) keyEquivalent:@"q"] autorelease];
    quit.target = self;
    quit.keyEquivalentModifierMask = NSEventModifierFlagCommand;
    [self.rightMenu addItem:quit];
    [self applyView:@{}];
    kuriGoReady();
}

- (void)applyView:(NSDictionary *)view {
    NSAssert([NSThread isMainThread], @"AppKit must run on the main thread.");
    self.view = view;
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

- (void)showRightMenu:(id)sender {
    NSStatusBarButton *button = self.item.button;
    [self.rightMenu popUpMenuPositioningItem:nil atLocation:NSMakePoint(0, NSHeight(button.bounds)) inView:button];
}

- (void)changeServer:(id)sender {
    kuriGoAction([self.view[@"mode"] isEqualToString:@"stopped"] ? 1 : 2);
}

- (void)quit:(id)sender { kuriGoAction(3); }
- (void)menuWillOpen:(NSMenu *)menu { kuriGoVisible(1); }
- (void)menuDidClose:(NSMenu *)menu { kuriGoVisible(0); }
- (NSApplicationTerminateReply)applicationShouldTerminate:(NSApplication *)sender {
    kuriGoAction(3);
    return NSTerminateCancel;
}
@end

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
    NSString *text = [[NSString alloc] initWithUTF8String:json];
    dispatch_async(dispatch_get_main_queue(), ^{
        NSDictionary *view = [NSJSONSerialization JSONObjectWithData:[text dataUsingEncoding:NSUTF8StringEncoding] options:0 error:NULL];
        if ([view isKindOfClass:[NSDictionary class]]) [application applyView:view];
    });
    [text release];
}

void kuriTerminate(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        [[NSStatusBar systemStatusBar] removeStatusItem:application.item];
        NSApp.delegate = nil;
        [NSApp terminate:nil];
    });
}
