// Ports apps/p2-mac/Sources/P2Mac/ServerConfig.swift
// Which local p2 server the app uses and how to run it: user defaults, overridden by P2MAC_* environment variables.
import Foundation

struct ServerConfig: Equatable {
	enum Source: Equatable {
		/// The app runs the server from `checkout` (or adopts one already on `port`).
		case own
		/// The app uses a server someone else runs (a checkout's in a terminal) and never starts or stops it.
		case attached
	}

	var source: Source
	/// The `apps/p2` directory of a checkout; nil until the user picks one.
	var checkout: URL?
	/// An absolute p2 binary; nil: the checkout's `p2` (`go build ./cmd/p2` there), else `p2` on the login shell's PATH.
	var p2: String?
	/// The server's port: the attached server's, or the one the app runs its own on.
	var port: Int
	/// The server's data directory: its `owner-key` signs the app in.
	var dataDir: URL
	/// `P2_FAKE=1`: the faux provider, for demos and tests.
	var fake: Bool

	var origin: URL { URL(string: "http://127.0.0.1:\(port)")! }

	static let defaultDataDir = FileManager.default.homeDirectoryForCurrentUser.appending(path: ".pi/p2")

	/// The configuration of this launch. Environment variables win, so tests and dev runs never touch saved settings.
	static func load() -> ServerConfig {
		let env = ProcessInfo.processInfo.environment
		let defaults = UserDefaults.standard
		let value = { (key: String, variable: String) in env[variable] ?? defaults.string(forKey: key) }
		// A server chosen in the menu wins over the environment's own-server settings; P2MAC_ATTACH wins over both.
		let attachedFromEnv = env["P2MAC_ATTACH"].flatMap(Int.init)
		let attachedPort = attachedFromEnv
			?? (defaults.string(forKey: "source") == "attached" ? defaults.string(forKey: "attachedPort").flatMap(Int.init) : nil)
		let attachedDataDir = attachedFromEnv != nil ? env["P2MAC_DATA_DIR"] : defaults.string(forKey: "attachedDataDir")
		return ServerConfig(
			source: attachedPort == nil ? .own : .attached,
			checkout: value("checkout", "P2MAC_CHECKOUT").map(path).flatMap(p2Directory),
			p2: value("p2", "P2MAC_P2"),
			port: attachedPort ?? value("port", "P2MAC_PORT").flatMap(Int.init) ?? 8788,
			dataDir: (attachedPort == nil ? value("dataDir", "P2MAC_DATA_DIR") : attachedDataDir).map(path) ?? defaultDataDir,
			fake: value("fake", "P2MAC_FAKE") == "1"
		)
	}

	/// Remembers to use the server on `port` with the data directory `dataDir` from now on.
	static func saveAttached(port: Int, dataDir: URL) {
		UserDefaults.standard.set("attached", forKey: "source")
		UserDefaults.standard.set(String(port), forKey: "attachedPort")
		UserDefaults.standard.set(dataDir.path, forKey: "attachedDataDir")
	}

	/// Remembers to run the app's own server from now on.
	static func saveOwn() {
		UserDefaults.standard.set("own", forKey: "source")
	}

	/// Remembers the checkout the user picked.
	static func saveCheckout(_ directory: URL) {
		UserDefaults.standard.set(directory.path, forKey: "checkout")
	}

	/// The `apps/p2` directory for a picked folder: p2 itself, or a repository root that contains it.
	static func p2Directory(_ folder: URL) -> URL? {
		for candidate in [folder, folder.appending(path: "apps/p2")] {
			if FileManager.default.fileExists(atPath: candidate.appending(path: "cmd/p2/main.go").path) {
				return candidate
			}
		}
		return nil
	}

	private static func path(_ text: String) -> URL {
		URL(fileURLWithPath: (text as NSString).expandingTildeInPath)
	}
}
