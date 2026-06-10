import SwiftUI
import AppKit
import Combine

class SakanaWindowController: NSWindowController {
    private let animator: SakanaAnimator
    private let settings: SettingsStore
    private var hostingView: NSHostingView<SakanaWidgetView>?
    private var cancellables = Set<AnyCancellable>()

    init(animator: SakanaAnimator, settings: SettingsStore, initialMetrics: ViewportMetrics) {
        self.animator = animator
        self.settings = settings

        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: initialMetrics.width, height: initialMetrics.height),
            styleMask: [.borderless],
            backing: .buffered,
            defer: false
        )
        window.isOpaque = false
        window.backgroundColor = .clear
        window.level = settings.alwaysOnTop ? .floating : .normal
        window.collectionBehavior = [.canJoinAllSpaces, .stationary]
        window.isMovableByWindowBackground = false
        window.hasShadow = false
        window.ignoresMouseEvents = false

        super.init(window: window)

        let rootView = SakanaWidgetView(animator: animator, settings: settings)
        let hosting = NSHostingView(rootView: rootView)
        hosting.autoresizingMask = [.width, .height]
        window.contentView = hosting
        self.hostingView = hosting

        // 监听图片变化，自动更新视窗
        settings.$characterImage
            .receive(on: DispatchQueue.main)
            .sink { [weak self] _ in
                self?.updateViewport()
            }
            .store(in: &cancellables)
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) has not been implemented")
    }

    func updateWindowLevel() {
        window?.level = settings.alwaysOnTop ? .floating : .normal
    }

    func refreshView() {
        guard let hosting = hostingView else { return }
        hosting.rootView = SakanaWidgetView(animator: animator, settings: settings)
    }

    /// 根据当前图片重新计算视窗大小并调整窗口
    func updateViewport() {
        guard let window = window else { return }

        let metrics = calculateViewport(for: settings.characterImage)
        let newSize = NSSize(width: metrics.width, height: metrics.height)

        // 更新物理引擎的 size
        animator.updateSize(Double(metrics.imageSize))

        // 保持窗口底部中心位置不变，调整大小
        let oldFrame = window.frame
        let bottomCenterX = oldFrame.midX
        let bottomY = oldFrame.minY

        let newOrigin = NSPoint(
            x: bottomCenterX - newSize.width / 2,
            y: bottomY
        )

        let newFrame = NSRect(origin: newOrigin, size: newSize)
        window.setFrame(newFrame, display: true, animate: false)

        // SwiftUI 的 @ObservedObject 会自动重绘视图
    }
}
