import SwiftUI
import AppKit

class AppDelegate: NSObject, NSApplicationDelegate {
    var windowController: SakanaWindowController?
    var settingsWindowController: NSWindowController?
    let settings = SettingsStore()
    lazy var animator: SakanaAnimator = {
        let metrics = calculateViewport(for: settings.characterImage)
        return SakanaAnimator(imageSize: Double(metrics.imageSize))
    }()

    func applicationDidFinishLaunching(_ notification: Notification) {
        let metrics = calculateViewport(for: settings.characterImage)
        windowController = SakanaWindowController(
            animator: animator,
            settings: settings,
            initialMetrics: metrics
        )
        positionWindow()
        windowController?.showWindow(nil)
        animator.start()
        setAppIcon()
        showSettings()
    }

    private func setAppIcon() {
        if let url = Bundle.module.url(forResource: "AppIcon", withExtension: "icns"),
           let image = NSImage(contentsOf: url) {
            NSApp.applicationIconImage = image
        }
    }

    func showSettings() {
        NSApp.setActivationPolicy(.regular)
        if settingsWindowController == nil {
            let window = NSWindow(
                contentRect: NSRect(x: 0, y: 0, width: 380, height: 360),
                styleMask: [.titled, .closable, .miniaturizable],
                backing: .buffered,
                defer: false
            )
            window.title = "Sakana Settings"
            window.contentView = NSHostingView(
                rootView: SettingsView(settings: settings) { [weak self] in
                    self?.applySettings()
                }
            )
            settingsWindowController = NSWindowController(window: window)
        }
        settingsWindowController?.showWindow(nil)
        settingsWindowController?.window?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    func positionWindow() {
        guard let window = windowController?.window else { return }
        let screen = resolveScreen()
        let screenFrame = screen.visibleFrame
        let windowFrame = window.frame

        let x: CGFloat
        let y: CGFloat
        if settings.positionX == 0 && settings.positionY == 0 {
            x = screenFrame.maxX - windowFrame.width - 64
            y = screenFrame.minY + 64
            settings.positionX = Double(x)
            settings.positionY = Double(y)
        } else {
            x = CGFloat(settings.positionX)
            y = CGFloat(settings.positionY)
        }
        window.setFrameOrigin(NSPoint(x: x, y: y))
    }

    private func resolveScreen() -> NSScreen {
        if !settings.screenName.isEmpty,
           let target = NSScreen.screens.first(where: { $0.localizedName == settings.screenName }) {
            return target
        }
        return NSScreen.main ?? NSScreen.screens.first!
    }

    func applySettings() {
        windowController?.updateWindowLevel()
        positionWindow()
    }
}

@main
struct SakanaApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) var appDelegate

    var body: some Scene {
        MenuBarExtra("Sakana", systemImage: "fish.fill") {
            Button("Settings...") {
                appDelegate.showSettings()
            }
            .keyboardShortcut(",", modifiers: .command)

            Divider()

            Button("About Sakana") {
                NSApp.orderFrontStandardAboutPanel()
            }

            Divider()

            Button("Quit") {
                NSApplication.shared.terminate(nil)
            }
            .keyboardShortcut("q", modifiers: .command)
        }
    }
}
