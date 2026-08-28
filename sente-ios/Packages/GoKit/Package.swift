// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "GoKit",
    platforms: [.iOS(.v17), .macOS(.v14)],
    products: [
        .library(name: "GoKit", targets: ["GoKit"])
    ],
    targets: [
        .target(
            name: "GoKit",
            swiftSettings: [.swiftLanguageMode(.v6)]
        ),
        .testTarget(
            name: "GoKitTests",
            dependencies: ["GoKit"],
            swiftSettings: [.swiftLanguageMode(.v6)]
        )
    ]
)
