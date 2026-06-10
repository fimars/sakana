import SwiftUI

struct SakanaWidgetView: View {
    @ObservedObject var animator: SakanaAnimator
    @ObservedObject var settings: SettingsStore

    /// 根据当前图片动态计算布局参数
    private var metrics: ViewportMetrics {
        calculateViewport(for: settings.characterImage)
    }

    var body: some View {
        let r = animator.state.r
        let y = animator.state.y
        let m = metrics

        GeometryReader { geo in
            let midX = geo.size.width / 2
            let baseTopY = geo.size.height - m.baseHeight - m.basePad
            let rad = r * .pi / 180
            let charAttachX = midX + sin(rad) * m.rodLength
            let charAttachY = baseTopY - cos(rad) * m.rodLength + y * m.rodVerticalScale

            ZStack {
                Path { path in
                    path.move(to: CGPoint(x: midX, y: baseTopY))
                    path.addLine(to: CGPoint(x: charAttachX, y: charAttachY))
                }
                .stroke(Color.gray.opacity(0.5), lineWidth: 2)

                CharacterImageView(
                    image: settings.characterImage,
                    rotation: r,
                    size: m.imageSize
                )
                .position(x: charAttachX, y: charAttachY - m.imageSize / 2)

                RoundedRectangle(cornerRadius: 4)
                    .fill(Color.gray.opacity(0.4))
                    .frame(width: m.baseWidth, height: m.baseHeight)
                    .position(x: midX, y: geo.size.height - m.baseHeight / 2 - m.basePad)
            }
            .contentShape(Rectangle())
        }
        .frame(width: m.width, height: m.height)
        .gesture(
            DragGesture(minimumDistance: 0)
                .onChanged { value in
                    animator.drag(deltaX: value.translation.width, deltaY: value.translation.height)
                }
                .onEnded { _ in
                    animator.endDrag()
                }
        )
    }
}
