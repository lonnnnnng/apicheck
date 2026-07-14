//go:build darwin

package main

/*
#cgo darwin CFLAGS: -x objective-c
#cgo darwin LDFLAGS: -framework Cocoa -framework WebKit -framework QuartzCore
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#import <QuartzCore/QuartzCore.h>

static void configureViewTree(NSView *view, CGFloat radius) {
    if (view == nil) {
        return;
    }
    view.wantsLayer = YES;
    view.layer.backgroundColor = NSColor.clearColor.CGColor;
    if ([view isKindOfClass:[WKWebView class]]) {
        [((WKWebView *)view) setValue:@NO forKey:@"drawsBackground"];
        view.layer.cornerRadius = radius;
        view.layer.masksToBounds = YES;
    }
    for (NSView *child in view.subviews) {
        configureViewTree(child, radius);
    }
}

static void applyRoundedTransparentWindowNow(CGFloat radius) {
    for (NSWindow *window in NSApp.windows) {
        if (window == nil) {
            continue;
        }

        window.opaque = NO;
        window.backgroundColor = NSColor.clearColor;
        window.hasShadow = YES;

        NSView *contentView = window.contentView;
        if (contentView == nil) {
            return;
        }
        contentView.wantsLayer = YES;
        contentView.layer.backgroundColor = NSColor.clearColor.CGColor;
        contentView.layer.cornerRadius = radius;
        contentView.layer.masksToBounds = YES;
        configureViewTree(contentView, radius);
    }
}

static void ApplyRoundedTransparentWindow(double radius) {
    dispatch_async(dispatch_get_main_queue(), ^{
        applyRoundedTransparentWindowNow((CGFloat)radius);
    });
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(250 * NSEC_PER_MSEC)), dispatch_get_main_queue(), ^{
        applyRoundedTransparentWindowNow((CGFloat)radius);
    });
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(1000 * NSEC_PER_MSEC)), dispatch_get_main_queue(), ^{
        applyRoundedTransparentWindowNow((CGFloat)radius);
    });
}
*/
import "C"

func applyRoundedTransparentWindow(radius float64) {
	// 作者 long: Wails 的透明选项只能影响 WebView 背景，macOS 无边框窗口仍可能保留矩形底色；
	// 这里在主线程直接调整 NSWindow 和内容视图，确保四角真正透出桌面背景。
	C.ApplyRoundedTransparentWindow(C.double(radius))
}
