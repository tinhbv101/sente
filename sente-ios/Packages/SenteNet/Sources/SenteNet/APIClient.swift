import Foundation

public enum APIError: Error, Equatable, Sendable {
    /// The server's own error envelope (docs/06 §1.1). `message` is already
    /// localised and safe to show.
    case server(code: String, message: String, status: Int)
    case unauthorized
    case transport(String)
    case decoding(String)

    public var userMessage: String {
        switch self {
        case .server(_, let message, _): return message
        case .unauthorized: return String(localized: "Phiên đăng nhập đã hết hạn.", bundle: SenteNetL10n.bundle())
        case .transport: return String(localized: "Không kết nối được máy chủ.", bundle: SenteNetL10n.bundle())
        case .decoding: return String(localized: "Máy chủ trả về dữ liệu không đọc được.", bundle: SenteNetL10n.bundle())
        }
    }
}

/// REST client for everything that is not realtime.
public actor APIClient {
    public let baseURL: URL
    private let session: URLSession
    private var accessToken: String?
    private var refreshToken: String?
    /// Called whenever the pair changes, so the caller can persist it.
    private var onTokens: (@Sendable (String, String?) -> Void)?
    private var refreshing: Task<Bool, Never>?

    public init(baseURL: URL, session: URLSession = .shared, accessToken: String? = nil,
                refreshToken: String? = nil) {
        self.baseURL = baseURL
        self.session = session
        self.accessToken = accessToken
        self.refreshToken = refreshToken
    }

    public func setAccessToken(_ token: String?) { accessToken = token }
    public func setTokens(access: String?, refresh: String?) { accessToken = access; refreshToken = refresh }
    public func onTokensChanged(_ handler: @escaping @Sendable (String, String?) -> Void) { onTokens = handler }
    public var token: String? { accessToken }
    public var hasRefreshToken: Bool { refreshToken != nil }

    /// Exchanges the refresh token for a new pair. False means the session is
    /// gone for good (expired, revoked, or the account deleted) and the caller
    /// should start over as a new guest.
    public func refresh() async -> Bool {
        // Concurrent 401s share one refresh: a second rotation would spend the
        // token the first one just received.
        if let refreshing { return await refreshing.value }
        let task = Task<Bool, Never> { [refreshToken] in
            guard let refreshToken else { return false }
            struct Body: Encodable { let refreshToken: String }
            do {
                let (data, _) = try await perform("POST", "/v1/auth/refresh", body: Body(refreshToken: refreshToken),
                                                  authenticated: false, allowRefresh: false)
                let session = try ProtocolDecoder.json.decode(Session.self, from: data)
                accessToken = session.accessToken
                self.refreshToken = session.refreshToken
                onTokens?(session.accessToken, session.refreshToken)
                return true
            } catch {
                return false
            }
        }
        refreshing = task
        defer { refreshing = nil }
        return await task.value
    }

    public func logout() async {
        if let refreshToken {
            struct Body: Encodable { let refreshToken: String }
            _ = try? await perform("POST", "/v1/auth/logout", body: Body(refreshToken: refreshToken),
                                   authenticated: false, allowRefresh: false)
        }
        accessToken = nil
        refreshToken = nil
    }

    // MARK: - Endpoints

    public func config() async throws -> ServerConfig {
        try await request("GET", "/v1/config", authenticated: false)
    }

    /// Creates a guest account and keeps its tokens for later calls.
    public func signUpGuest() async throws -> GuestSignUp {
        let result: GuestSignUp = try await request("POST", "/v1/auth/guest", authenticated: false)
        accessToken = result.accessToken
        refreshToken = result.refreshToken
        onTokens?(result.accessToken, result.refreshToken)
        return result
    }

    /// Exchanges an Apple identity token for a session. With a token on hand the
    /// Apple ID is linked to the current guest account, so its games survive.
    public func signInWithApple(identityToken: String, nonce: String, fullName: String?) async throws -> GuestSignUp {
        struct Body: Encodable { let identityToken: String; let nonce: String; let fullName: String? }
        let signUp: GuestSignUp = try await request("POST", "/v1/auth/apple",
                                                    body: Body(identityToken: identityToken, nonce: nonce, fullName: fullName),
                                                    authenticated: accessToken != nil)
        accessToken = signUp.accessToken
        refreshToken = signUp.refreshToken
        onTokens?(signUp.accessToken, signUp.refreshToken)
        return signUp
    }

    public func registerDevice(token: String, environment: String, appVersion: String?) async throws {
        struct Body: Encodable { let apnsToken: String; let environment: String; let appVersion: String? }
        _ = try await perform("POST", "/v1/devices",
                              body: Body(apnsToken: token, environment: environment, appVersion: appVersion),
                              authenticated: true)
    }

    public func unregisterDevice(token: String) async throws {
        try await requestNoContent("DELETE", "/v1/devices/\(token)")
    }

    public func me() async throws -> GuestSignUp.User {
        try await request("GET", "/v1/me")
    }

    public func rename(_ displayName: String) async throws -> GuestSignUp.User {
        struct Body: Encodable { let displayName: String }
        return try await request("PATCH", "/v1/me", body: Body(displayName: displayName))
    }

    public func deleteAccount() async throws {
        try await requestNoContent("DELETE", "/v1/me")
        accessToken = nil
        refreshToken = nil
    }

    public func moves(gameID: String) async throws -> GameMoves {
        try await request("GET", "/v1/games/\(gameID)/moves")
    }

    public func report(userID: String, gameID: String?, category: String, note: String) async throws {
        struct Body: Encodable { let userId: String; let gameId: String?; let category: String; let note: String }
        _ = try await perform("POST", "/v1/reports",
                              body: Body(userId: userID, gameId: gameID, category: category, note: note),
                              authenticated: true, allowRefresh: true)
    }

    public func block(userID: String) async throws {
        struct Body: Encodable { let userId: String }
        _ = try await perform("POST", "/v1/blocks", body: Body(userId: userID), authenticated: true, allowRefresh: true)
    }

    public func myGames() async throws -> [GameSummary] {
        struct Envelope: Decodable { let items: [GameSummary] }
        return try await request("GET", "/v1/games", as: Envelope.self).items
    }

    public func game(_ id: String) async throws -> GameStatePayload {
        try await request("GET", "/v1/games/\(id)")
    }

    public func createChallenge(_ config: GameConfigRequest) async throws -> Challenge {
        try await request("POST", "/v1/challenges", body: config)
    }

    public func myChallenges() async throws -> [Challenge] {
        struct Envelope: Decodable { let items: [Challenge] }
        return try await request("GET", "/v1/challenges", as: Envelope.self).items
    }

    /// Preview needs no token: the link is meant to be read before signing up.
    public func challenge(code: String) async throws -> Challenge {
        try await request("GET", "/v1/challenges/\(code)", authenticated: false)
    }

    public func acceptChallenge(code: String) async throws -> AcceptedChallenge {
        try await request("POST", "/v1/challenges/\(code)/accept")
    }

    public func declineChallenge(code: String) async throws {
        try await requestNoContent("POST", "/v1/challenges/\(code)/decline")
    }

    public func cancelChallenge(code: String) async throws {
        try await requestNoContent("DELETE", "/v1/challenges/\(code)")
    }

    // MARK: - Plumbing

    private func request<T: Decodable>(_ method: String, _ path: String,
                                       authenticated: Bool = true) async throws -> T {
        try await request(method, path, body: Optional<Int>.none, authenticated: authenticated, as: T.self)
    }

    private func request<T: Decodable>(_ method: String, _ path: String, as: T.Type) async throws -> T {
        try await request(method, path, body: Optional<Int>.none, authenticated: true, as: T.self)
    }

    private func request<T: Decodable, B: Encodable>(_ method: String, _ path: String, body: B,
                                                     authenticated: Bool = true) async throws -> T {
        try await request(method, path, body: Optional(body), authenticated: authenticated, as: T.self)
    }

    private func request<T: Decodable, B: Encodable>(_ method: String, _ path: String, body: B?,
                                                     authenticated: Bool, as: T.Type) async throws -> T {
        let (data, status) = try await perform(method, path, body: body, authenticated: authenticated)
        do {
            return try ProtocolDecoder.json.decode(T.self, from: data)
        } catch {
            throw APIError.decoding("\(status) \(path): \(error)")
        }
    }

    private func requestNoContent(_ method: String, _ path: String) async throws {
        _ = try await perform(method, path, body: Optional<Int>.none, authenticated: true)
    }

    private func perform<B: Encodable>(_ method: String, _ path: String, body: B?,
                                        authenticated: Bool) async throws -> (Data, Int) {
        try await perform(method, path, body: body, authenticated: authenticated, allowRefresh: true)
    }

    /// One transparent retry after a 401: refresh, then repeat the request. Only
    /// once, so a token the server keeps rejecting cannot loop forever.
    private func perform<B: Encodable>(_ method: String, _ path: String, body: B?,
                                        authenticated: Bool, allowRefresh: Bool) async throws -> (Data, Int) {
        do {
            return try await performOnce(method, path, body: body, authenticated: authenticated)
        } catch APIError.unauthorized where authenticated && allowRefresh && refreshToken != nil {
            guard await refresh() else { throw APIError.unauthorized }
            return try await performOnce(method, path, body: body, authenticated: authenticated)
        }
    }

    private func performOnce<B: Encodable>(_ method: String, _ path: String, body: B?,
                                            authenticated: Bool) async throws -> (Data, Int) {
        var urlRequest = URLRequest(url: baseURL.appending(path: path))
        urlRequest.httpMethod = method
        urlRequest.timeoutInterval = 15
        urlRequest.setValue("application/json", forHTTPHeaderField: "Accept")
        if let body {
            urlRequest.httpBody = try ProtocolDecoder.encoder.encode(body)
            urlRequest.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        if authenticated, let accessToken {
            urlRequest.setValue("Bearer \(accessToken)", forHTTPHeaderField: "Authorization")
        }

        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await session.data(for: urlRequest)
        } catch {
            throw APIError.transport(error.localizedDescription)
        }
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(status) else {
            if status == 401 { throw APIError.unauthorized }
            if let envelope = try? ProtocolDecoder.json.decode(ErrorEnvelope.self, from: data) {
                throw APIError.server(code: envelope.error.code, message: envelope.error.message, status: status)
            }
            throw APIError.server(code: "http_\(status)", message: String(localized: "Máy chủ trả về lỗi \(status).", bundle: SenteNetL10n.bundle()), status: status)
        }
        return (data, status)
    }
}

/// The server's error shape, identical for every status (docs/06 §1.1).
private struct ErrorEnvelope: Decodable { let error: ErrorPayload }
