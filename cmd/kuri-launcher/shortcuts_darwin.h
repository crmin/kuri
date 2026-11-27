#ifndef KURI_SHORTCUTS_H
#define KURI_SHORTCUTS_H

#import <AppKit/AppKit.h>

enum { KuriKeyQ = 12, KuriKeyW = 13 };

static inline BOOL kuriCommandKey(NSEvent *event, unsigned short keyCode, NSString *character) {
    NSEventModifierFlags modifiers = NSEventModifierFlagCommand | NSEventModifierFlagShift |
        NSEventModifierFlagOption | NSEventModifierFlagControl;
    return event.type == NSEventTypeKeyDown && !event.isARepeat &&
        (event.modifierFlags & modifiers) == NSEventModifierFlagCommand &&
        (event.keyCode == keyCode || [event.charactersIgnoringModifiers.lowercaseString isEqualToString:character]);
}

#endif
