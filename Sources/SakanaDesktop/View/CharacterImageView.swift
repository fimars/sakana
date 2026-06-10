import SwiftUI

struct CharacterImageView: View {
    let image: NSImage?
    var rotation: Double
    var size: Double

    var body: some View {
        Group {
            if let image = image {
                Image(nsImage: image)
                    .resizable()
                    .aspectRatio(contentMode: .fit)
            } else {
                Image(systemName: "fish.fill")
                    .resizable()
                    .aspectRatio(contentMode: .fit)
                    .foregroundStyle(.blue.gradient)
            }
        }
        .frame(width: size, height: size)
        .rotationEffect(.degrees(rotation), anchor: .bottom)
    }
}
