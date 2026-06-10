import SwiftUI
import AppKit

class SakanaAnimator: ObservableObject {
    @Published var state: SakanaState

    private var displayLink: CADisplayLink?
    private var lastTimestamp: CFTimeInterval = 0
    private var isDragging = false

    init(imageSize: Double) {
        state = SakanaState(size: imageSize)
    }

    func start() {
        state.startKick()
        if let screen = NSScreen.main {
            displayLink = screen.displayLink(target: self, selector: #selector(tick))
        }
        if #available(macOS 15.0, *) {
            displayLink?.preferredFrameRateRange = CAFrameRateRange(minimum: 30, maximum: 60, preferred: 60)
        }
        displayLink?.add(to: .main, forMode: .common)
        lastTimestamp = CACurrentMediaTime()
    }

    func updateSize(_ size: Double) {
        state = SakanaState(size: size)
        state.startKick()
    }

    func stop() {
        displayLink?.invalidate()
        displayLink = nil
    }

    @objc private func tick(_ link: CADisplayLink) {
        guard !isDragging else { return }
        let now = CACurrentMediaTime()
        let delta = now - lastTimestamp
        lastTimestamp = now
        state.update(deltaTime: delta)
    }

    func drag(deltaX: Double, deltaY: Double) {
        if !isDragging {
            isDragging = true
        }
        state.drag(deltaX: deltaX, deltaY: deltaY)
    }

    func endDrag() {
        isDragging = false
    }
}
