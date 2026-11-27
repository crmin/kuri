#ifndef KURI_SHORTCUTS_H
#define KURI_SHORTCUTS_H

#import <AppKit/AppKit.h>

enum { KuriKeyQ = 12, KuriKeyW = 13 };

static inline BOOL kuriCommandKey(NSEvent *event, unsigned short keyCode, NSString *character) {
    NSEventModifierFlags modifiers = NSEventModifierFlagCommand | NSEventModifierFlagShift |
        NSEventModifierFlagOption | NSEventModifierFlagControl;
    if (event.type != NSEventTypeKeyDown || event.isARepeat ||
        (event.modifierFlags & modifiers) != NSEventModifierFlagCommand) return NO;
    NSString *logical = event.charactersIgnoringModifiers.lowercaseString;
    if ([logical isEqualToString:character]) return YES;
    if (logical.length == 1) {
        unichar value = [logical characterAtIndex:0];
        if (value >= 0x20 && value <= 0x7e) return NO;
    }
    if (logical.length && [logical rangeOfString:@"\\A\\p{Latin}\\p{M}*\\z" options:NSRegularExpressionSearch].location != NSNotFound) return NO;
    return event.keyCode == keyCode;
}

#endif
