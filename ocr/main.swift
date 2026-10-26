// jarvis-ocr: 사진 한 장에 쓰인 글자를 읽는다 - 애플 Vision, 한국어와 영어.
//
// search-svc 가 읽은 페이지의 그림(공지 카드, 상품 상세 이미지, 포스터)을 여기로 보낸다. Vision 은
// macOS 의 것이라 컨테이너에서는 못 쓰고, 그래서 이것 하나는 tts 처럼 맥미니 호스트에서
// LaunchAgent(com.jarvis.ocr)로 돈다. 컨테이너는 host.docker.internal:8107 로 닿는다.
//
//   GET  /health   {"ok":true}  토큰 불필요
//   POST /ocr      본문 = 이미지 바이트(JPEG·PNG·GIF·HEIC…)
//                  → {"width":…, "height":…, "ms":…, "lines":[{"text":…, "conf":…}]}
//
// 세로로 아주 긴 그림(상품 상세)은 Vision 이 줄여서 보느라 글자를 놓친다. 자르는 것은 보내는
// 쪽(search-svc)이 하고, 여기는 받은 한 장을 그대로 읽는다.
//
// 토큰은 Authorization: Bearer <JARVIS_SEARCH_TOKEN>(환경변수, 없으면 --env-file 의 값).
// 루프백에만 묶는다 - 같은 와이파이의 다른 기기는 이 포트를 못 본다.

import Foundation
import ImageIO
import Network
import Vision

struct Options {
    var host = "127.0.0.1"
    var port: UInt16 = 8107
    var envFile: String?
}

func parseOptions() -> Options {
    var o = Options()
    var args = CommandLine.arguments.dropFirst().makeIterator()
    while let a = args.next() {
        switch a {
        case "--host": if let v = args.next() { o.host = v }
        case "--port": if let v = args.next(), let p = UInt16(v) { o.port = p }
        case "--env-file": o.envFile = args.next()
        default:
            log("모르는 인자: \(a)")
            exit(2)
        }
    }
    return o
}

func log(_ s: String) {
    let f = ISO8601DateFormatter()
    f.timeZone = TimeZone(identifier: "Asia/Seoul")
    print("\(f.string(from: Date())) \(s)")
    fflush(stdout)
}

/// The token: JARVIS_SEARCH_TOKEN from the environment, else from the .env
/// file the services read (KEY=VALUE lines, quotes allowed).
func loadToken(_ envFile: String?) -> String {
    if let v = ProcessInfo.processInfo.environment["JARVIS_SEARCH_TOKEN"], !v.isEmpty { return v }
    guard let path = envFile, let text = try? String(contentsOfFile: path, encoding: .utf8) else { return "" }
    for raw in text.split(separator: "\n") {
        let line = raw.trimmingCharacters(in: .whitespaces)
        guard line.hasPrefix("JARVIS_SEARCH_TOKEN=") else { continue }
        var v = String(line.dropFirst("JARVIS_SEARCH_TOKEN=".count)).trimmingCharacters(in: .whitespaces)
        if v.count >= 2, let f = v.first, f == "\"" || f == "'", v.last == f { v = String(v.dropFirst().dropLast()) }
        return v
    }
    return ""
}

struct Line: Codable {
    let text: String
    let conf: Float
}

struct Answer: Codable {
    let width: Int
    let height: Int
    let ms: Int
    let lines: [Line]
}

enum OCRError: Error { case badImage }

func recognize(_ data: Data) throws -> Answer {
    let start = Date()
    guard let src = CGImageSourceCreateWithData(data as CFData, nil),
          let image = CGImageSourceCreateImageAtIndex(src, 0, nil) else { throw OCRError.badImage }
    let req = VNRecognizeTextRequest()
    req.recognitionLevel = .accurate
    req.recognitionLanguages = ["ko-KR", "en-US"]
    req.usesLanguageCorrection = true
    try VNImageRequestHandler(cgImage: image, options: [:]).perform([req])
    // Vision's own order: a block's lines together, a label before its value.
    let lines = (req.results ?? []).compactMap { o -> Line? in
        guard let c = o.topCandidates(1).first else { return nil }
        return Line(text: c.string, conf: c.confidence)
    }
    return Answer(width: image.width, height: image.height, ms: Int(Date().timeIntervalSince(start) * 1000), lines: lines)
}

/// One connection's bytes so far. A class so the receive callbacks share it.
final class Conn {
    let c: NWConnection
    var buf = Data()
    var done = false
    init(_ c: NWConnection) { self.c = c }
}

final class Server {
    let token: Data
    var listener: NWListener?
    let queue = DispatchQueue(label: "jarvis.ocr", attributes: .concurrent)
    // Four at once: the Mac mini also runs the services and the voice.
    let slots = DispatchSemaphore(value: 4)
    static let maxBody = 30 << 20
    static let maxHead = 16 << 10

    init(token: String) { self.token = Data(token.utf8) }

    func start(_ o: Options) throws {
        let params = NWParameters.tcp
        params.allowLocalEndpointReuse = true
        params.requiredLocalEndpoint = .hostPort(host: NWEndpoint.Host(o.host), port: NWEndpoint.Port(rawValue: o.port)!)
        let listener = try NWListener(using: params)
        listener.stateUpdateHandler = { state in
            switch state {
            case .ready: log("듣는 중 http://\(o.host):\(o.port)")
            case .failed(let e):
                log("포트를 열지 못했습니다: \(e)")
                exit(1)
            default: break
            }
        }
        listener.newConnectionHandler = { [weak self] c in self?.accept(c) }
        listener.start(queue: queue)
        self.listener = listener
    }

    func accept(_ nc: NWConnection) {
        let conn = Conn(nc)
        nc.start(queue: queue)
        // A client that never finishes its request is dropped.
        queue.asyncAfter(deadline: .now() + 60) { if !conn.done { nc.cancel() } }
        receive(conn)
    }

    func receive(_ conn: Conn) {
        conn.c.receive(minimumIncompleteLength: 1, maximumLength: 1 << 16) { [weak self] data, _, complete, error in
            guard let self else { return }
            if let data { conn.buf.append(data) }
            if error != nil { conn.c.cancel(); return }
            guard let sep = conn.buf.range(of: Data("\r\n\r\n".utf8)) else {
                if conn.buf.count > Server.maxHead { self.reply(conn, 431, Note(error: "머리가 너무 깁니다")) } else if complete { conn.c.cancel() } else { self.receive(conn) }
                return
            }
            let head = String(decoding: conn.buf[..<sep.lowerBound], as: UTF8.self)
            var lines = head.components(separatedBy: "\r\n")
            let request = lines.removeFirst().split(separator: " ")
            var headers: [String: String] = [:]
            for l in lines {
                guard let i = l.firstIndex(of: ":") else { continue }
                headers[l[..<i].lowercased()] = l[l.index(after: i)...].trimmingCharacters(in: .whitespaces)
            }
            let method = request.first.map(String.init) ?? ""
            let path = request.count > 1 ? String(request[1].split(separator: "?").first ?? "") : ""
            let length = Int(headers["content-length"] ?? "0") ?? -1
            if length < 0 || length > Server.maxBody { self.reply(conn, 413, Note(error: "본문이 너무 큽니다")); return }
            let have = conn.buf.count - sep.upperBound
            if have < length {
                if complete { conn.c.cancel() } else { self.receive(conn) }
                return
            }
            let body = conn.buf.subdata(in: sep.upperBound..<(sep.upperBound + length))
            self.route(conn, method, path, headers, body)
        }
    }

    func route(_ conn: Conn, _ method: String, _ path: String, _ headers: [String: String], _ body: Data) {
        switch (method, path) {
        case ("GET", "/health"):
            reply(conn, 200, Note(ok: true))
        case ("POST", "/ocr"):
            guard authorized(headers["authorization"] ?? "") else { reply(conn, 401, Note(error: "토큰이 맞지 않습니다")); return }
            guard !body.isEmpty else { reply(conn, 400, Note(error: "이미지가 없습니다")); return }
            slots.wait()
            defer { slots.signal() }
            do {
                let a = try recognize(body)
                reply(conn, 200, a)
            } catch OCRError.badImage {
                reply(conn, 415, Note(error: "이미지로 읽지 못했습니다"))
            } catch {
                log("읽지 못했습니다: \(error)")
                reply(conn, 500, Note(error: "글자를 읽지 못했습니다: \(error.localizedDescription)"))
            }
        default:
            reply(conn, 404, Note(error: "없는 경로입니다"))
        }
    }

    func authorized(_ header: String) -> Bool {
        let given = Data(header.utf8)
        let want = Data("Bearer ".utf8) + token
        guard !token.isEmpty, given.count == want.count else { return false }
        var diff: UInt8 = 0
        for (a, b) in zip(given, want) { diff |= a ^ b }
        return diff == 0
    }

    func reply<T: Encodable>(_ conn: Conn, _ status: Int, _ value: T) {
        let body = (try? JSONEncoder().encode(value)) ?? Data("{}".utf8)
        let reason = [200: "OK", 400: "Bad Request", 401: "Unauthorized", 404: "Not Found", 413: "Payload Too Large",
                      415: "Unsupported Media Type", 431: "Request Header Fields Too Large", 500: "Internal Server Error"][status] ?? "Status"
        let head = "HTTP/1.1 \(status) \(reason)\r\nContent-Type: application/json; charset=utf-8\r\nContent-Length: \(body.count)\r\nConnection: close\r\n\r\n"
        conn.done = true
        conn.c.send(content: Data(head.utf8) + body, completion: .contentProcessed { _ in conn.c.cancel() })
    }
}

/// A reply that is not a reading: {"ok":true} or {"error":"…"}.
struct Note: Codable {
    var ok: Bool?
    var error: String?
}

let options = parseOptions()
let token = loadToken(options.envFile)
if token.isEmpty {
    log("JARVIS_SEARCH_TOKEN 이 없습니다. 환경변수나 --env-file 로 주세요.")
    exit(1)
}
let server = Server(token: token)
do {
    try server.start(options)
} catch {
    log("시작하지 못했습니다: \(error)")
    exit(1)
}
dispatchMain()
