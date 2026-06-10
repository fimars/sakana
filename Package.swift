// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "SakanaDesktop",
    platforms: [.macOS(.v14)],
    targets: [
        .executableTarget(
            name: "SakanaDesktop",
            path: "Sources/SakanaDesktop",
            resources: [.process("Resources")]
        )
    ]
)
