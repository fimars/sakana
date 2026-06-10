import SwiftUI
import AppKit

class SettingsStore: ObservableObject {
    @AppStorage("alwaysOnTop") var alwaysOnTop: Bool = false
    @AppStorage("screenName") var screenName: String = ""
    @AppStorage("positionX") var positionX: Double = 0
    @AppStorage("positionY") var positionY: Double = 0

    @Published var characterImage: NSImage? = nil

    private var bookmarkData: Data? {
        get { UserDefaults.standard.data(forKey: "characterImageBookmark") }
        set { UserDefaults.standard.set(newValue, forKey: "characterImageBookmark") }
    }

    init() {
        loadCharacterImage()
        if characterImage == nil {
            characterImage = bundledDefaultImage()
        }
    }

    private func bundledDefaultImage() -> NSImage? {
        guard let url = Bundle.module.url(forResource: "takina", withExtension: "png") else { return nil }
        return NSImage(contentsOf: url)
    }

    func loadCharacterImage() {
        guard let data = bookmarkData else { return }
        var isStale = false
        guard let url = try? URL(
            resolvingBookmarkData: data,
            options: .withSecurityScope,
            bookmarkDataIsStale: &isStale
        ) else { return }
        _ = url.startAccessingSecurityScopedResource()
        characterImage = NSImage(contentsOf: url)
        url.stopAccessingSecurityScopedResource()
    }

    func saveImageBookmark(from url: URL) {
        guard let data = try? url.bookmarkData(
            options: .withSecurityScope,
            includingResourceValuesForKeys: nil
        ) else { return }
        bookmarkData = data
        loadCharacterImage()
    }

    func removeCharacterImage() {
        bookmarkData = nil
        characterImage = bundledDefaultImage()
    }
}
