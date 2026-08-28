// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "SenteNet",
    platforms: [.iOS(.v17), .macOS(.v14)],
    products: [.library(name: "SenteNet", targets: ["SenteNet"])],
    dependencies: [.package(path: "../GoKit")],
    targets: [
        .target(name: "SenteNet", dependencies: ["GoKit"],
                swiftSettings: [.swiftLanguageMode(.v6)]),
        .testTarget(name: "SenteNetTests", dependencies: ["SenteNet"],
                    swiftSettings: [.swiftLanguageMode(.v6)])
    ]
)
