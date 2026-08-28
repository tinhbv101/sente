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
        case .unauthorized: return "Phiên đăng nhập đã hết hạn."
        case .transport: return "Không kết nối được máy chủ."
        case .decoding: return "Máy chủ trả về dữ liệu không đọc được."
        }
    }
}

/// REST client for everything that is not realtime.
public actor APIClient {
    public let baseURL: URL
    private let session: URLSession
    private var accessToken: String?

    public init(baseURL: URL, session: URLSession = .shared, accessToken: String? = nil) {
        self.baseURL = baseURL
        self.session = session
        self.accessToken = accessToken
    }

    public func setAccessToken(_ token: String?) { accessToken = token }
    public var token: String? { accessToken }

    // MARK: - Endpoints

    public func config() async throws -> ServerConfig {
        try await request("GET", "/v1/config", authenticated: false)
    }

    /// Creates a guest account and keeps its token for later calls.
    public func signUpGuest() async throws -> GuestSignUp {
        let result: GuestSignUp = try await request("POST", "/v1/auth/guest", authenticated: false)
        accessToken = result.accessToken
        return result
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
            throw APIError.server(code: "http_\(status)", message: "Máy chủ trả về lỗi \(status).", status: status)
        }
        return (data, status)
    }
}

/// The server's error shape, identical for every status (docs/06 §1.1).
private struct ErrorEnvelope: Decodable { let error: ErrorPayload }
