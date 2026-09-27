import Foundation

/// Product-level backend choices. The Go CLI remains the source of truth for
/// protocol behavior; the macOS layer only presents a stable selection model.
enum NJUConnectBackend: String, CaseIterable, Identifiable, Hashable, Codable {
    case easyConnect = "easyconnect"
    case aTrust = "atrust"

    var id: String { rawValue }

    var title: String {
        switch self {
        case .easyConnect: return "EasyConnect"
        case .aTrust: return "aTrust"
        }
    }

}

enum NJUConnectAuthenticationCapability: String, Codable, Hashable {
    case sharedPassword = "shared_password"
    case verificationCode = "verification_code"
    case oauth
}

struct NJUConnectBackendDescriptor: Codable, Equatable, Identifiable {
    let id: NJUConnectBackend
    let displayName: String
    let shortName: String
    let defaultGateway: String
    let authentication: [NJUConnectAuthenticationCapability]

    private enum CodingKeys: String, CodingKey {
        case id
        case displayName = "display_name"
        case shortName = "short_name"
        case defaultGateway = "default_gateway"
        case authentication
    }
}

struct NJUConnectBackendCatalog: Codable, Equatable {
    let schemaVersion: Int
    let socksListen: String
    let backends: [NJUConnectBackendDescriptor]

    private enum CodingKeys: String, CodingKey {
        case schemaVersion = "schema_version"
        case socksListen = "socks_listen"
        case backends
    }

    func descriptor(for backend: NJUConnectBackend) -> NJUConnectBackendDescriptor? {
        backends.first { $0.id == backend }
    }
}
