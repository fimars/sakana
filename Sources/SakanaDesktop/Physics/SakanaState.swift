import Foundation

struct SakanaState {
    let inertia: Double = 0.08
    let stickiness: Double = 0.1
    let decay: Double = 0.99

    var size: Double

    var r: Double = 0
    var y: Double = 0
    var t: Double = 0
    var w: Double = 0

    var maxR: Double { min(max(size / 5, 30), 60) }
    var maxY: Double { size / 4 }

    init(size: Double) {
        self.size = size
    }

    mutating func startKick() {
        r = 15
        y = 10
    }

    mutating func update(deltaTime: TimeInterval) {
        let frameDiff = deltaTime * 1000
        let effInertia = frameDiff <= 16 ? inertia * frameDiff / 16.67 : inertia

        w -= r * 2
        r += w * effInertia * 1.2
        w *= decay

        t -= y * 2
        y += t * effInertia * 2
        t *= decay
    }

    mutating func drag(deltaX: Double, deltaY: Double) {
        w = 0
        t = 0
        r = (deltaX * stickiness).clamped(to: -maxR...maxR)
        y = (deltaY * stickiness * 2).clamped(to: -maxY...maxY)
    }

    var isResting: Bool {
        max(abs(w), abs(r), abs(t), abs(y)) < 0.1
    }
}

extension Comparable {
    func clamped(to limits: ClosedRange<Self>) -> Self {
        min(max(self, limits.lowerBound), limits.upperBound)
    }
}
