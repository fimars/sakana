#import <Cocoa/Cocoa.h>
#import <QuartzCore/QuartzCore.h>
#import <ImageIO/ImageIO.h>
#import <math.h>

static CALayer *gOverlay = nil;
static CALayer *gCharacter = nil;

void ca_sway_cancel(void) {
    if (!gOverlay) {
        return;
    }
    [CATransaction begin];
    [CATransaction setDisableActions:YES];
    [gOverlay removeFromSuperlayer];
    gOverlay = nil;
    gCharacter = nil;
    [CATransaction commit];
}

int ca_sway_start(uintptr_t winPtr, const void *img, size_t imgLen,
                  double midX, double baseTopY, double baseWidth, double baseHeight,
                  double rodLength, double rodVerticalScale, double imageSize,
                  const double *rs, const double *ys, int count, double duration) {
    NSWindow *win = (__bridge NSWindow *)(void *)winPtr;
    if (!win || !img || !rs || !ys || count < 2 || rodLength <= 0 || duration <= 0) {
        return 0;
    }
    NSView *content = win.contentView;
    if (!content) {
        return 0;
    }
    if (!content.layer) {
        content.wantsLayer = YES;
    }
    if (!content.layer) {
        return 0;
    }

    NSData *data = [NSData dataWithBytes:img length:imgLen];
    CGImageSourceRef src = CGImageSourceCreateWithData((__bridge CFDataRef)data, NULL);
    if (!src) {
        return 0;
    }
    CGImageRef image = CGImageSourceCreateImageAtIndex(src, 0, NULL);
    CFRelease(src);
    if (!image) {
        return 0;
    }

    ca_sway_cancel();

    // The Mygo surface holds the last frame it drew. Clear it so the old
    // character does not show through the moving layers.
    for (NSView *v in content.subviews) {
        if ([NSStringFromClass([v class]) isEqualToString:@"MyGoSurfaceView"]) {
            v.layer.contents = nil;
        }
    }

    [CATransaction begin];
    [CATransaction setDisableActions:YES];

    CALayer *container = [CALayer layer];
    container.frame = content.bounds;
    container.geometryFlipped = YES;
    container.zPosition = 1000;
    container.masksToBounds = NO;
    [content.layer addSublayer:container];
    gOverlay = container;

    CALayer *base = [CALayer layer];
    base.frame = CGRectMake(midX - baseWidth / 2.0, baseTopY, baseWidth, baseHeight);
    base.backgroundColor = [NSColor colorWithSRGBRed:128.0 / 255.0 green:128.0 / 255.0 blue:128.0 / 255.0 alpha:0.4].CGColor;
    base.cornerRadius = 4;
    [container addSublayer:base];

    CAShapeLayer *rod = [CAShapeLayer layer];
    rod.bounds = CGRectMake(0, 0, 2, rodLength);
    rod.anchorPoint = CGPointMake(0.5, 0);
    rod.position = CGPointMake(midX, baseTopY);
    CGMutablePathRef path = CGPathCreateMutable();
    CGPathMoveToPoint(path, NULL, 1, 0);
    CGPathAddLineToPoint(path, NULL, 1, rodLength);
    rod.path = path;
    CGPathRelease(path);
    rod.strokeColor = [NSColor colorWithSRGBRed:128.0 / 255.0 green:128.0 / 255.0 blue:128.0 / 255.0 alpha:0.5].CGColor;
    rod.lineWidth = 2;
    rod.lineCap = kCALineCapButt;
    rod.fillColor = nil;
    [container addSublayer:rod];

    CALayer *character = [CALayer layer];
    character.bounds = CGRectMake(0, 0, imageSize, imageSize);
    character.anchorPoint = CGPointMake(0.5, 0);
    character.contentsGravity = kCAGravityResize;
    character.contentsScale = win.backingScaleFactor;
    character.contents = (__bridge id)image;
    [container addSublayer:character];
    gCharacter = character;

    NSMutableArray *rot = [NSMutableArray arrayWithCapacity:count];
    NSMutableArray *posX = [NSMutableArray arrayWithCapacity:count];
    NSMutableArray *posY = [NSMutableArray arrayWithCapacity:count];
    NSMutableArray *rodRot = [NSMutableArray arrayWithCapacity:count];
    NSMutableArray *rodScale = [NSMutableArray arrayWithCapacity:count];

    for (int i = 0; i < count; i++) {
        double angle = rs[i] * M_PI / 180.0;
        double attachX = midX + sin(angle) * rodLength;
        double attachY = baseTopY - cos(angle) * rodLength + ys[i] * rodVerticalScale;
        double dx = attachX - midX;
        double dy = baseTopY - attachY;
        double rodAngle = atan2(dx, dy);
        double rodLen = hypot(dx, dy);

        [rot addObject:@(angle)];
        [posX addObject:@(attachX)];
        [posY addObject:@(attachY)];
        [rodRot addObject:@(rodAngle)];
        [rodScale addObject:@(rodLen / rodLength)];
    }

    [character setValue:rot.firstObject forKeyPath:@"transform.rotation.z"];
    [character setValue:posX.firstObject forKeyPath:@"position.x"];
    [character setValue:posY.firstObject forKeyPath:@"position.y"];
    [rod setValue:rodRot.firstObject forKeyPath:@"transform.rotation.z"];
    [rod setValue:rodScale.firstObject forKeyPath:@"transform.scale.y"];

    CAKeyframeAnimation *(^make)(NSString *, NSArray *) = ^(NSString *key, NSArray *values) {
        CAKeyframeAnimation *a = [CAKeyframeAnimation animationWithKeyPath:key];
        a.values = values;
        a.duration = duration;
        a.calculationMode = kCAAnimationLinear;
        a.fillMode = kCAFillModeForwards;
        a.removedOnCompletion = NO;
        return a;
    };

    [character addAnimation:make(@"transform.rotation.z", rot) forKey:@"sakana.rot"];
    [character addAnimation:make(@"position.x", posX) forKey:@"sakana.x"];
    [character addAnimation:make(@"position.y", posY) forKey:@"sakana.y"];
    [rod addAnimation:make(@"transform.rotation.z", rodRot) forKey:@"sakana.rod.rot"];
    [rod addAnimation:make(@"transform.scale.y", rodScale) forKey:@"sakana.rod.len"];

    [CATransaction commit];
    CGImageRelease(image);
    return 1;
}

// ca_sway_presentation reads the animated character's presentation layer:
// the pose the compositor is showing, not the layer's model pose. It is for
// the harness, which cannot capture a presentation pose with CapturePage.
int ca_sway_presentation(double *r, double *x, double *y) {
    if (!gCharacter) {
        return 0;
    }
    CALayer *p = [gCharacter presentationLayer];
    if (!p) {
        return 0;
    }
    CATransform3D t = p.transform;
    *r = atan2(t.m12, t.m11) * 180.0 / M_PI;
    CGPoint pos = p.position;
    *x = pos.x;
    *y = pos.y;
    return 1;
}
