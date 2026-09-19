// swift-tools-version: 5.9

import PackageDescription

let package = Package(
    name: "TaterTubeServer",
    platforms: [
        .macOS("14.0")
    ],
    products: [
        .executable(name: "TaterTubeServer", targets: ["TaterTubeServer"])
    ],
    targets: [
        .executableTarget(
            name: "TaterTubeServer",
            path: "Sources/TaterTubeServer",
            linkerSettings: [
                .linkedFramework("AppKit"),
                .linkedFramework("ServiceManagement")
            ]
        )
    ]
)
