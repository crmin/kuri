#import <AppKit/AppKit.h>

NSRect kuriServiceMenuFrame(NSRect parent, NSRect anchor, NSRect usable, NSSize size);

@interface KuriPopover : NSViewController <NSPopoverDelegate>
- (void)toggleAt:(NSStatusBarButton *)button;
- (void)close;
- (void)applyView:(NSDictionary *)view;
@end
