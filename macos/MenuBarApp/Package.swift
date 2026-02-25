// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "NornicDBMenuBarApp",
    platforms: [
        .macOS(.v12),
    ],
    products: [
        .executable(name: "NornicDB", targets: ["NornicDB"]),
    ],
    dependencies: [
        .package(url: "https://github.com/jpsim/Yams.git", from: "5.1.3"),
    ],
    targets: [
        .executableTarget(
            name: "NornicDB",
            dependencies: [
                .product(name: "Yams", package: "Yams"),
            ],
            path: ".",
            exclude: [
                "ConfigParserTest.swift",
                "EmbeddingServerTest.swift",
                "EmbeddingServerSettingsView.swift",
                "test-embedding-server.sh",
            ],
            sources: [
                "NornicDBMenuBar.swift",
                "FileIndexer.swift",
                "FileIndexerWindow.swift",
                "FileIndexerSupport.swift",
                "FileIndexerFileBrowserView.swift",
                "AppleMLEmbedder.swift",
                "EmbeddingServer.swift",
            ]
        ),
    ]
)
