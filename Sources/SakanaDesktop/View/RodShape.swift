import SwiftUI

struct RodShape: Shape {
    var angle: Double
    var offsetY: Double
    var rodLength: Double

    func path(in rect: CGRect) -> Path {
        var path = Path()
        let startX = rect.midX
        let startY = rect.maxY
        let rad = angle * .pi / 180
        let endX = startX + sin(rad) * rodLength
        let endY = startY - cos(rad) * rodLength + offsetY * 0.3

        path.move(to: CGPoint(x: startX, y: startY))
        path.addLine(to: CGPoint(x: endX, y: endY))
        return path
    }
}
