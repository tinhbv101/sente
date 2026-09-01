import XCTest
import SenteNet
@testable import Sente

/// Switching the in-app language must re-route every kind of string at once:
/// app strings, package strings, lesson content — and back to the device default.
@MainActor
final class AppLanguageTests: XCTestCase {
    override func tearDown() {
        LanguageManager.apply(.system)
    }

    func testOverridesApplyWithoutARestart() {
        LanguageManager.apply(.en)
        XCTAssertEqual(LS(localized: "Đóng"), "Close")
        XCTAssertEqual(TimeLimits.format(ms: 3 * 3_600_000), "3 h", "package strings follow")
        XCTAssertFalse(LanguageManager.isVietnamese)

        LanguageManager.apply(.vi)
        XCTAssertEqual(LS(localized: "Đóng"), "Đóng")
        XCTAssertEqual(TimeLimits.format(ms: 3 * 3_600_000), "3 giờ")
        XCTAssertTrue(LanguageManager.isVietnamese)

        let lesson = LessonLibrary.shared.chapters[0].lessons[0].steps[0]
        LanguageManager.apply(.en)
        XCTAssertEqual(lesson.text.text, lesson.text.en, "lesson content follows the switch")
        LanguageManager.apply(.vi)
        XCTAssertEqual(lesson.text.text, lesson.text.vi)
    }

    func testSystemRestoresTheDeviceChoice() {
        LanguageManager.apply(.vi)
        LanguageManager.apply(.system)
        let appDomain = UserDefaults.standard.persistentDomain(forName: Bundle.main.bundleIdentifier!)
        XCTAssertNil(appDomain?["AppleLanguages"], "system mode must not pin AppleLanguages")
        XCTAssertEqual(LS(localized: "Đóng"), String(localized: "Đóng"),
                       "system mode matches plain lookups")
    }

    func testTheChoiceRoundTripsThroughSettings() {
        var settings = Settings.load()
        settings.language = .en
        settings.save()
        XCTAssertEqual(Settings.load().language, .en)
        settings.language = .system
        settings.save()
        XCTAssertEqual(Settings.load().language, .system)
    }
}
