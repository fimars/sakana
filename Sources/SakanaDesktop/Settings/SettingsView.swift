import SwiftUI
import AppKit

// MARK: - Coordinate Pad

/// A 128×128 coordinate pad with origin at the **bottom-right** corner (ergonomic).
/// The user drags a metallic handle to set x/y offsets from the bottom-right of the screen.
struct CoordinatePadView: View {
    @Binding var x: Double
    @Binding var y: Double

    let maxX: Double
    let maxY: Double

    private let padSize: CGFloat = 256
    private let handleSize: CGFloat = 10
    private let gridStep: CGFloat = 32  // canvas pixels per grid line
    private let axisColor = Color(red: 0.820, green: 0.851, blue: 0.878)  // #d1d9e0
    private let tickColor = Color(red: 0.925, green: 0.933, blue: 0.941)  // #eceef0

    /// Handle center in view coordinates (origin top-left).
    /// Maps (x=0,y=0) → bottom-right corner, (x=maxX,y=maxY) → top-left corner.
    private var handleCenter: CGPoint {
        let hh = handleSize / 2
        let ratioX = maxX > 0 ? min(max(x / maxX, 0), 1) : 0
        let ratioY = maxY > 0 ? min(max(y / maxY, 0), 1) : 0
        return CGPoint(
            x: hh + (padSize - handleSize) * CGFloat(1 - ratioX),
            y: hh + (padSize - handleSize) * CGFloat(1 - ratioY)
        )
    }

    var body: some View {
        HStack(alignment: .bottom, spacing: 4) {
            ZStack(alignment: .topLeading) {
                // Grid canvas
                Canvas { context, size in
                    // White background
                    context.fill(
                        Path(CGRect(origin: .zero, size: CGSize(width: padSize, height: padSize))),
                        with: .color(.white)
                    )

                    // Grid lines every 32px of canvas, in #fafbfc
                    var x: CGFloat = gridStep
                    while x < padSize {
                        var line = Path()
                        line.move(to: CGPoint(x: x, y: 0))
                        line.addLine(to: CGPoint(x: x, y: padSize))
                        context.stroke(line, with: .color(tickColor), lineWidth: 0.5)
                        x += gridStep
                    }

                    var y: CGFloat = gridStep
                    while y < padSize {
                        var line = Path()
                        line.move(to: CGPoint(x: 0, y: y))
                        line.addLine(to: CGPoint(x: padSize, y: y))
                        context.stroke(line, with: .color(tickColor), lineWidth: 0.5)
                        y += gridStep
                    }

                    // Border in #d1d9e0
                    context.stroke(
                        Path(CGRect(origin: .zero, size: CGSize(width: padSize - 0.5, height: padSize - 0.5))),
                        with: .color(axisColor),
                        lineWidth: 1.5
                    )
                }
                .frame(width: padSize, height: padSize)

                // Metallic drag handle
                Circle()
                    .fill(.white)
                    .frame(width: handleSize, height: handleSize)
                    .overlay(
                        Circle()
                            .stroke(Color.gray.opacity(0.4), lineWidth: 0.5)
                    )
                    .shadow(color: .black.opacity(0.18), radius: 1.5, x: 0.5, y: 1)
                    .position(handleCenter)
            }
            .frame(width: padSize, height: padSize)

            // (0,0) label — placed in "quadrant II" (right of origin)
            Text("(0,0)")
                .font(.system(size: 9))
                .foregroundColor(Color(white: 0.55))
                .padding(.bottom, 4)
        }
        .gesture(
            DragGesture(minimumDistance: 0)
                .onChanged { value in
                    let hh = handleSize / 2
                    let cx = max(hh, min(padSize - hh, value.location.x))
                    let cy = max(hh, min(padSize - hh, value.location.y))
                    let range = padSize - handleSize  // = padSize - 2*hh
                    let ratioX = Double((padSize - hh - cx) / range)
                    let ratioY = Double((padSize - hh - cy) / range)
                    x = ratioX * maxX
                    y = ratioY * maxY
                }
        )
    }
}

// MARK: - Settings View

struct SettingsView: View {
    @ObservedObject var settings: SettingsStore
    var onApply: (() -> Void)?

    @State private var padX: Double = 64
    @State private var padY: Double = 64
    @State private var padMaxX: Double = 2560
    @State private var padMaxY: Double = 1440

    /// The active screen (respects the picker).
    private var targetScreen: NSScreen {
        if !settings.screenName.isEmpty,
           let match = NSScreen.screens.first(where: { $0.localizedName == settings.screenName }) {
            return match
        }
        return NSScreen.main ?? NSScreen.screens.first!
    }

    /// X distance from the **right edge** of the target screen.
    /// Sentinel (0,0) in absolute coords → show sensible defaults.
    private var displayX: Double {
        if settings.positionX == 0 && settings.positionY == 0 {
            return 64
        }
        let metrics = calculateViewport(for: settings.characterImage)
        return targetScreen.visibleFrame.maxX - settings.positionX - metrics.width
    }

    /// Y distance from the **bottom edge** of the target screen.
    private var displayY: Double {
        if settings.positionX == 0 && settings.positionY == 0 {
            return 64
        }
        return settings.positionY - targetScreen.visibleFrame.minY
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            // Character Image
            VStack(alignment: .leading, spacing: 8) {
                Text("Character Image")
                    .font(.headline)
                HStack {
                    if let image = settings.characterImage {
                        Image(nsImage: image)
                            .resizable()
                            .aspectRatio(contentMode: .fit)
                            .frame(width: 60, height: 60)
                    } else {
                        Image(systemName: "fish.fill")
                            .font(.largeTitle)
                            .foregroundStyle(.blue.gradient)
                            .frame(width: 60, height: 60)
                    }
                    Button("Choose Image...") {
                        pickImage()
                    }
                    Button("Remove") {
                        settings.removeCharacterImage()
                    }
                }
            }

            Divider()

            // Window
            VStack(alignment: .leading, spacing: 8) {
                Text("Window")
                    .font(.headline)
                Toggle("Always on Top", isOn: $settings.alwaysOnTop)
            }

            Divider()

            // Position
            VStack(alignment: .leading, spacing: 8) {
                Text("Position")
                    .font(.headline)

                Picker("Screen", selection: $settings.screenName) {
                    Text("Primary Display").tag("")
                    ForEach(NSScreen.screens, id: \.localizedName) { screen in
                        Text(screen.localizedName).tag(screen.localizedName)
                    }
                }

                HStack(alignment: .top, spacing: 12) {
                    CoordinatePadView(
                        x: $padX,
                        y: $padY,
                        maxX: padMaxX,
                        maxY: padMaxY
                    )

                    VStack(alignment: .leading, spacing: 4) {
                        Text("Offset from")
                            .font(.caption)
                            .foregroundColor(.secondary)
                        Text("bottom-right corner")
                            .font(.caption)
                            .foregroundColor(.secondary)
                        Spacer().frame(height: 8)
                        Text("x: \(Int(padX.rounded()))")
                            .font(.caption.monospaced())
                        Text("y: \(Int(padY.rounded()))")
                            .font(.caption.monospaced())
                    }
                }
            }

            Spacer()

            HStack {
                Spacer()
                Button("Apply") {
                    let frame = targetScreen.visibleFrame
                    let metrics = calculateViewport(for: settings.characterImage)
                    settings.positionX = frame.maxX - padX - metrics.width
                    settings.positionY = frame.minY + padY
                    onApply?()
                }
                .keyboardShortcut(.defaultAction)
            }
        }
        .frame(minWidth: 420, minHeight: 560)
        .padding()
        .onAppear {
            padX = displayX
            padY = displayY
            padMaxX = targetScreen.visibleFrame.width
            padMaxY = targetScreen.visibleFrame.height
        }
    }

    private func pickImage() {
        let panel = NSOpenPanel()
        panel.allowedContentTypes = [.png, .jpeg]
        panel.allowsMultipleSelection = false
        panel.canChooseFiles = true
        panel.begin { response in
            if response == .OK, let url = panel.url {
                settings.saveImageBookmark(from: url)
            }
        }
    }
}
