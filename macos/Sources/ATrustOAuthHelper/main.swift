import AppKit
import Darwin
import Foundation
import WebKit

private let soundConnectOAuthStoreIdentifier = UUID(uuidString: "B7D4B8D9-63AD-4A75-9D17-8A4D0F4A2D6B")!

private struct Options {
    let clearData: Bool
    let loginURL: URL?
    let gatewayHost: String?
    let gatewayPort: Int?

    static func parse(_ arguments: [String]) -> Options? {
        if arguments == ["--clear-data"] {
            return Options(clearData: true, loginURL: nil, gatewayHost: nil, gatewayPort: nil)
        }
        var values: [String: String] = [:]
        var index = 0
        while index + 1 < arguments.count {
            values[arguments[index]] = arguments[index + 1]
            index += 2
        }
        guard index == arguments.count,
              let rawLoginURL = values["--login-url"],
              let loginURL = URL(string: rawLoginURL),
              loginURL.scheme == "https",
              let gatewayHost = values["--gateway-host"],
              !gatewayHost.isEmpty,
              let rawPort = values["--gateway-port"],
              let gatewayPort = Int(rawPort),
              (1 ... 65_535).contains(gatewayPort)
        else {
            return nil
        }
        return Options(clearData: false, loginURL: loginURL, gatewayHost: gatewayHost, gatewayPort: gatewayPort)
    }
}

@MainActor
private final class OAuthWindowController: NSObject, NSApplicationDelegate, NSWindowDelegate, WKNavigationDelegate {
    private let options: Options
    private var window: NSWindow?
    private var completed = false
    private var revealWorkItem: DispatchWorkItem?
    private(set) var exitCode: Int32 = 1

    init(options: Options) {
        self.options = options
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        let configuration = WKWebViewConfiguration()
        // Keep this profile isolated from Safari and other applications while
        // retaining the NJU SSO cookies between aTrust reconnects. The Go
        // side still stores the aTrust client data separately in Keychain.
        if #available(macOS 14.0, *) {
            configuration.websiteDataStore = WKWebsiteDataStore(forIdentifier: soundConnectOAuthStoreIdentifier)
        } else {
            // macOS 13 has no identifier-based store; its default WebKit
            // profile is still persistent and remains scoped to this app.
            configuration.websiteDataStore = .default()
        }
        if options.clearData {
            clearPersistentData(configuration.websiteDataStore)
            return
        }
        guard let loginURL = options.loginURL else {
            fail("OAuth login URL is unavailable.\n")
            return
        }
        let webView = WKWebView(frame: .zero, configuration: configuration)
        webView.navigationDelegate = self

        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 720, height: 760),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered,
            defer: false
        )
        window.title = "SoundConnect aTrust Login"
        window.contentView = webView
        window.delegate = self
        window.center()
        // A valid SSO cookie normally reaches the callback without any user
        // interaction. Keep the window hidden until a real login page remains
        // visible, so reconnects do not flash a browser window.
        window.orderOut(nil)
        self.window = window
        webView.load(URLRequest(url: loginURL))
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        guard !completed,
              let url = webView.url,
              url.path == "/authserver/login"
        else {
            return
        }

        revealWorkItem?.cancel()
        let workItem = DispatchWorkItem { [weak self, weak webView] in
            guard let self,
                  !self.completed,
                  let currentURL = webView?.url,
                  currentURL.path == "/authserver/login"
            else {
                return
            }
            self.showLoginWindow()
        }
        revealWorkItem = workItem
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.6, execute: workItem)
    }

    func webView(
        _ webView: WKWebView,
        decidePolicyFor navigationAction: WKNavigationAction,
        decisionHandler: @escaping @MainActor @Sendable (WKNavigationActionPolicy) -> Void
    ) {
        guard let callback = navigationAction.request.url,
              callback.scheme == "https",
              callback.host?.lowercased() == options.gatewayHost?.lowercased(),
              callback.path == "/passport/v1/auth/httpsOauth2"
        else {
            decisionHandler(.allow)
            return
        }

        if let explicitPort = callback.port, explicitPort != options.gatewayPort {
            decisionHandler(.cancel)
            fail("OAuth callback port did not match the configured gateway.\n")
            return
        }
        guard let components = URLComponents(url: callback, resolvingAgainstBaseURL: false),
              let code = components.queryItems?.first(where: { $0.name == "code" })?.value,
              !code.isEmpty
        else {
            decisionHandler(.cancel)
            fail("OAuth callback did not contain an authorization code.\n")
            return
        }

        decisionHandler(.cancel)
        completed = true
        revealWorkItem?.cancel()
        exitCode = 0
        FileHandle.standardOutput.write(Data((code + "\n").utf8))
        NSApplication.shared.terminate(nil)
    }

    func windowWillClose(_ notification: Notification) {
        if !completed {
            NSApplication.shared.terminate(nil)
        }
    }

    private func showLoginWindow() {
        guard let window else {
            return
        }
        window.makeKeyAndOrderFront(nil)
        NSApplication.shared.activate(ignoringOtherApps: true)
    }

    private func clearPersistentData(_ store: WKWebsiteDataStore) {
        store.removeData(
            ofTypes: WKWebsiteDataStore.allWebsiteDataTypes(),
            modifiedSince: Date(timeIntervalSince1970: 0)
        ) { [weak self] in
            guard let self else {
                return
            }
            self.exitCode = 0
            NSApplication.shared.terminate(nil)
        }
    }

    private func fail(_ message: String) {
        revealWorkItem?.cancel()
        FileHandle.standardError.write(Data(message.utf8))
        NSApplication.shared.terminate(nil)
    }
}

guard let options = Options.parse(Array(CommandLine.arguments.dropFirst())) else {
    FileHandle.standardError.write(Data("Invalid OAuth helper arguments.\n".utf8))
    exit(2)
}
let application = NSApplication.shared
application.setActivationPolicy(.regular)
private let controller = OAuthWindowController(options: options)
application.delegate = controller
application.run()
exit(controller.exitCode)
