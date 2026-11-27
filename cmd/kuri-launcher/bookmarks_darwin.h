#ifndef KURI_BOOKMARKS_H
#define KURI_BOOKMARKS_H

#include <stdint.h>

void kuriRenderBookmarks(const char *json, uint64_t sequence);

#ifdef __OBJC__
#import <AppKit/AppKit.h>
@class KuriSettings;

extern NSNotificationName const KuriBookmarksDidChange;
void kuriRequestBookmarks(NSDictionary *request, void (^reply)(NSDictionary *response));

@interface KuriBookmarkSettings : NSObject
@property(nonatomic, readonly) NSView *view;
@property(nonatomic, readonly) BOOL saving;
- (instancetype)initWithSettings:(KuriSettings *)settings;
- (void)setAvailable:(BOOL)available;
- (void)reload;
- (void)addItem;
- (void)endEditing;
@end
#endif

#endif
