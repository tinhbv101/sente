import XCTest
import GoKit
@testable import SenteUI

final class BoardGeometryTests: XCTestCase {
    func testCentresAreEvenlySpacedAndSymmetric() {
        let g = BoardGeometry(size: 19, side: 380, showsCoordinates: false)
        let first = g.center(of: Point(col: 0, row: 0))
        let last = g.center(of: Point(col: 18, row: 18))
        XCTAssertEqual(first.x, g.padding, accuracy: 0.001)
        XCTAssertEqual(last.x, 380 - g.padding, accuracy: 0.001)
        XCTAssertEqual(g.center(of: Point(col: 1, row: 0)).x - first.x, g.cell, accuracy: 0.001)
    }

    func testHitTestingSnapsToTheNearestIntersection() {
        let g = BoardGeometry(size: 9, side: 360)
        let target = Point(col: 4, row: 4)
        let centre = g.center(of: target)
        XCTAssertEqual(g.point(at: centre), target)
        // Slightly off-centre still snaps.
        XCTAssertEqual(g.point(at: CGPoint(x: centre.x + g.cell * 0.4, y: centre.y - g.cell * 0.3)), target)
    }

    /// A touch further than 70% of a cell from any line is not a placement: it
    /// stops taps just off the board edge landing on the first line.
    func testFarTouchesAreRejected() {
        let g = BoardGeometry(size: 9, side: 360)
        XCTAssertNil(g.point(at: CGPoint(x: 2, y: 2)))
        XCTAssertNil(g.point(at: CGPoint(x: -20, y: 100)))
        XCTAssertNil(g.point(at: CGPoint(x: 360 + 5, y: 100)))
        let corner = g.center(of: Point(col: 0, row: 0))
        XCTAssertNil(g.point(at: CGPoint(x: corner.x - g.cell * 0.9, y: corner.y)))
    }

    func testEveryIntersectionRoundTripsThroughItsCentre() {
        for size in [9, 13, 19] {
            let g = BoardGeometry(size: size, side: 390)
            for point in Board(size: size).allPoints {
                XCTAssertEqual(g.point(at: g.center(of: point)), point, "\(size)x\(size) \(point)")
            }
        }
    }

    func testCoordinateLabelsMatchTheDisplayConvention() {
        let g = BoardGeometry(size: 19, side: 390)
        XCTAssertEqual(g.columnLabel(0), "A")
        XCTAssertEqual(g.columnLabel(8), "J", "I is skipped")
        XCTAssertEqual(g.rowLabel(0), "19", "rows count from the bottom")
        XCTAssertEqual(g.rowLabel(18), "1")
        XCTAssertEqual(g.starPoints.count, 9)
    }

    func testCoordinatesTakeMorePadding() {
        let with = BoardGeometry(size: 9, side: 300, showsCoordinates: true)
        let without = BoardGeometry(size: 9, side: 300, showsCoordinates: false)
        XCTAssertGreaterThan(with.padding, without.padding)
        XCTAssertLessThan(with.cell, without.cell)
    }
}
