import XCTest
@testable import Sente

final class AppleNameTests: XCTestCase {
    private func components(family: String?, middle: String?, given: String?) -> PersonNameComponents {
        var c = PersonNameComponents()
        c.familyName = family; c.middleName = middle; c.givenName = given
        return c
    }

    func testVietnameseReadsFamilyFirst() {
        let name = AppleName.displayName(components(family: "Bùi", middle: "Văn", given: "Tính"), locale: Locale(identifier: "vi_VN"))
        XCTAssertEqual(name, "Bùi Văn Tính")
    }

    func testOtherLocalesKeepTheSystemOrder() {
        let name = AppleName.displayName(components(family: "Smith", middle: nil, given: "Ada"), locale: Locale(identifier: "en_US"))
        XCTAssertEqual(name, "Ada Smith")
    }

    func testAppleSendingNothingYieldsNoName() {
        XCTAssertNil(AppleName.displayName(PersonNameComponents(), locale: Locale(identifier: "vi_VN")))
        XCTAssertNil(AppleName.displayName(components(family: " ", middle: nil, given: ""), locale: Locale(identifier: "vi_VN")))
    }
}
