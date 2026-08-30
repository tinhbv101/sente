import XCTest
@testable import Sente

final class InviteCodeTests: XCTestCase {
    func testEveryWayACodeArrivesIsRecognised() {
        for text in ["R4TN8KMP", "r4tn8kmp", " r4tn 8kmp ", "R4TN-8KMP",
                     "https://sente.devlord.net/j/R4TN8KMP", "https://sente.devlord.net/j/r4tn8kmp?utm=x",
                     "sente://j/R4TN8KMP"] {
            XCTAssertEqual(InviteCode.parse(text), "R4TN8KMP", text)
        }
    }

    func testWhatIsNotACodeIsRefused() {
        for text in ["", "R4TN8KM", "R4TN8KMPX", "R4TN8KM0", "R4TN8KMI",
                     "https://sente.devlord.net/g/R4TN8KMP", "https://evil.example/", "hello world"] {
            XCTAssertNil(InviteCode.parse(text), text)
        }
    }

    func testQRCarriesTheUniversalLinkWhenTheServerOffersOne() {
        XCTAssertEqual(InviteCode.link(for: "R4TN8KMP", shareUrl: "https://sente.devlord.net/j/R4TN8KMP"),
                       "https://sente.devlord.net/j/R4TN8KMP")
        XCTAssertEqual(InviteCode.link(for: "R4TN8KMP", shareUrl: nil), "sente://j/R4TN8KMP")
    }

    func testQRImageRendersAtTheRequestedSize() {
        let image = QRCode.image(for: "https://sente.devlord.net/j/R4TN8KMP", side: 120)
        XCTAssertNotNil(image)
        XCTAssertEqual(image?.size.width, 120)
        XCTAssertEqual(image?.size.width, image?.size.height)
    }
}
