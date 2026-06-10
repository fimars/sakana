import SwiftUI
import AppKit

struct SettingsView: View {
    @ObservedObject var settings: SettingsStore
    var onApply: (() -> Void)?

    @State private var xInput: String = ""
    @State private var yInput: String = ""

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

                HStack {
                    Text("x:")
                    TextField("", text: $xInput)
                        .textFieldStyle(.roundedBorder)
                        .multilineTextAlignment(.trailing)
                        .frame(width: 80)
                    Text("y:")
                    TextField("", text: $yInput)
                        .textFieldStyle(.roundedBorder)
                        .multilineTextAlignment(.trailing)
                        .frame(width: 80)
                }
            }

            Spacer()

            HStack {
                Spacer()
                Button("Apply") {
                    let frame = targetScreen.visibleFrame
                    let metrics = calculateViewport(for: settings.characterImage)
                    settings.positionX = frame.maxX - (Double(xInput) ?? displayX) - metrics.width
                    settings.positionY = frame.minY + (Double(yInput) ?? displayY)
                    onApply?()
                }
                .keyboardShortcut(.defaultAction)
            }
        }
        .frame(minWidth: 350, minHeight: 300)
        .padding()
        .onAppear {
            xInput = String(format: "%.0f", displayX)
            yInput = String(format: "%.0f", displayY)
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
