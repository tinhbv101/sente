import CoreImage
import CoreImage.CIFilterBuiltins
import UIKit

enum QRCode {
    /// Renders `text` as a crisp QR image. CoreImage draws one point per module;
    /// scaling with nearest-neighbour keeps the squares square.
    static func image(for text: String, side: CGFloat = 240) -> UIImage? {
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(text.utf8)
        filter.correctionLevel = "M"
        guard let output = filter.outputImage else { return nil }
        let scale = side / output.extent.width
        let scaled = output.transformed(by: CGAffineTransform(scaleX: scale, y: scale))
        guard let cgImage = CIContext().createCGImage(scaled, from: scaled.extent) else { return nil }
        return UIImage(cgImage: cgImage)
    }
}
