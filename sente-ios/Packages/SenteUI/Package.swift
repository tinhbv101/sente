// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "SenteUI",
    platforms: [.iOS(.v17), .macOS(.v14)],
    products: [.library(name: "SenteUI", targets: ["SenteUI"])],
    dependencies: [.package(path: "../GoKit")],
    targets: [
        .target(name: "SenteUI", dependencies: ["GoKit"],
                swiftSettings: [.swiftLanguageMode(.v6)]),
        .testTarget(name: "SenteUITests", dependencies: ["SenteUI"],
                    swiftSettings: [.swiftLanguageMode(.v6)])
    ]
)
