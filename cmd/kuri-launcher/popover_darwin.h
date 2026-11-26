#import <AppKit/AppKit.h>

@interface KuriPopover : NSViewController <NSPopoverDelegate>
- (void)toggleAt:(NSStatusBarButton *)button;
- (void)close;
- (void)applyView:(NSDictionary *)view;
@end
