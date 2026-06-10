import Foundation
import AppKit

struct ViewportMetrics {
    let width: CGFloat
    let height: CGFloat
    let imageSize: CGFloat
    let rodLength: CGFloat
    let baseWidth: CGFloat
    let baseHeight: CGFloat
    let basePad: CGFloat
    let rodVerticalScale: CGFloat
    let maxR: Double
    let maxY: Double

    var baseTopY: CGFloat { height - baseHeight - basePad }
    var midX: CGFloat { width / 2 }
}

/// 根据图片计算合适的显示大小和视窗大小
func calculateViewport(for image: NSImage?, maxImageSize: CGFloat = 120) -> ViewportMetrics {
    let imageSize = preferredImageSize(for: image, maxSize: maxImageSize)
    return calculateViewport(imageSize: imageSize)
}

/// 核心计算：给定图片显示大小，计算视窗所需尺寸
/// 使用现有物理算法（SakanaState.maxR / maxY）计算弹簧最大距离，
/// 然后计算图片在极限旋转时的边界框，最后加上安全边距。
func calculateViewport(imageSize: CGFloat) -> ViewportMetrics {
    let baseHeight: CGFloat = 6
    let basePad: CGFloat = 4
    let safetyMargin: CGFloat = 20

    // 支撑杆垂直位移系数（可下压系数）：0.3 → 0.6（调大一倍）
    let rodVerticalScale: CGFloat = 0.6

    // 物理参数 —— 使用现有算法（SakanaState 中的公式）
    let maxR = min(max(imageSize / 5, 30), 60) // 度
    let maxY = imageSize / 4

    // rodLength 保持与 imageSize 的当前比例：0.45 / 0.8 = 0.5625
    let rodLength = imageSize * 0.5625

    let rad = maxR * .pi / 180

    // 计算视窗宽度：
    // 2 * rod 水平偏移 + 图片在最大旋转时的宽度
    // 图片绕底部中心旋转 maxR 角后，最右点 X 偏移 = s/2*cos(maxR) + s*sin(maxR)
    let halfWidth = sin(rad) * rodLength + (imageSize / 2) * cos(rad) + imageSize * sin(rad)
    let width = 2 * halfWidth + safetyMargin

    // 计算视窗高度：
    // 底座(10) + rod 垂直长度 + 垂直弹性偏移 + 图片高度 + 安全边距
    // 保守估计：rod 垂直时图片最高点 = baseTopY - rodLength - maxY*rodVerticalScale - imageSize
    let height = basePad + baseHeight + rodLength + maxY * rodVerticalScale + imageSize + safetyMargin

    // baseWidth 保持与 imageSize 的原始比例：0.5 / 0.8 = 0.625
    let baseWidth = imageSize * 0.625

    return ViewportMetrics(
        width: width,
        height: height,
        imageSize: imageSize,
        rodLength: rodLength,
        baseWidth: baseWidth,
        baseHeight: baseHeight,
        basePad: basePad,
        rodVerticalScale: rodVerticalScale,
        maxR: maxR,
        maxY: maxY
    )
}

private func preferredImageSize(for image: NSImage?, maxSize: CGFloat) -> CGFloat {
    guard let image = image else { return maxSize }
    let minDimension = min(image.size.width, image.size.height)
    return min(minDimension, maxSize)
}
