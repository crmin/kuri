#ifndef KURI_SETTINGS_H
#define KURI_SETTINGS_H

void kuriRenderSettings(const char *json);
void kuriOpenSettings(void);
void kuriRequestQuit(void);

#ifdef __OBJC__
#import <AppKit/AppKit.h>

@interface KuriSettings : NSWindowController <NSWindowDelegate>
- (void)open;
- (void)applyResponse:(NSDictionary *)response;
- (void)requestQuit;
@end
#endif

#endif
