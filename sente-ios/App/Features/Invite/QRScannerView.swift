import AVFoundation
import SwiftUI
import SenteUI

/// Camera sheet that reads an invitation QR code and hands back the code.
struct QRScanSheet: View {
    let onCode: (String) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var permission: AVAuthorizationStatus = AVCaptureDevice.authorizationStatus(for: .video)
    @State private var found = false

    var body: some View {
        NavigationStack {
            ZStack {
                Tokens.ink.ignoresSafeArea()
                switch permission {
                case .authorized:
                    if AVCaptureDevice.default(for: .video) == nil {
                        message("Máy này không có camera.", systemImage: "camera.fill")
                    } else {
                        QRScannerView { payload in
                            // The camera keeps reporting the same code many times a second.
                            guard !found, let code = InviteCode.parse(payload) else { return }
                            found = true
                            onCode(code)
                        }
                        .ignoresSafeArea()
                        frame
                    }
                case .denied, .restricted:
                    message("Sente chưa được phép dùng camera.", systemImage: "camera.badge.ellipsis") {
                        Button("Mở Cài đặt") {
                            if let url = URL(string: UIApplication.openSettingsURLString) { UIApplication.shared.open(url) }
                        }.buttonStyle(PrimaryButton())
                    }
                default:
                    ProgressView().tint(.white)
                        .task {
                            let granted = await AVCaptureDevice.requestAccess(for: .video)
                            permission = granted ? .authorized : .denied
                        }
                }
            }
            .navigationTitle("Quét mã lời mời")
            .navigationBarTitleDisplayMode(.inline)
            .toolbarColorScheme(.dark, for: .navigationBar)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Đóng") { dismiss() } } }
        }
    }

    private var frame: some View {
        VStack {
            Spacer()
            RoundedRectangle(cornerRadius: 24)
                .strokeBorder(.white.opacity(0.9), lineWidth: 3)
                .frame(width: 240, height: 240)
            Text("Đưa mã QR của lời mời vào khung")
                .font(.footnote.weight(.medium)).foregroundStyle(.white)
                .padding(.top, 16)
            Spacer()
        }
    }

    private func message<Action: View>(_ text: String, systemImage: String,
                                       @ViewBuilder action: () -> Action = { EmptyView() }) -> some View {
        VStack(spacing: 16) {
            Image(systemName: systemImage).font(.system(size: 44)).foregroundStyle(.white.opacity(0.8))
            Text(text).foregroundStyle(.white).multilineTextAlignment(.center)
            action()
        }
        .padding(32)
    }
}

/// AVFoundation preview that reports QR payloads. Kept tiny: one session, one
/// output, no UI of its own.
struct QRScannerView: UIViewRepresentable {
    let onPayload: (String) -> Void

    func makeUIView(context: Context) -> PreviewView {
        let view = PreviewView()
        view.start(delegate: context.coordinator)
        return view
    }

    func updateUIView(_ uiView: PreviewView, context: Context) {}

    static func dismantleUIView(_ uiView: PreviewView, coordinator: Coordinator) { uiView.stop() }

    func makeCoordinator() -> Coordinator { Coordinator(onPayload: onPayload) }

    final class Coordinator: NSObject, AVCaptureMetadataOutputObjectsDelegate {
        let onPayload: (String) -> Void
        init(onPayload: @escaping (String) -> Void) { self.onPayload = onPayload }

        func metadataOutput(_ output: AVCaptureMetadataOutput, didOutput objects: [AVMetadataObject],
                            from connection: AVCaptureConnection) {
            for case let object as AVMetadataMachineReadableCodeObject in objects where object.type == .qr {
                if let payload = object.stringValue { onPayload(payload) }
            }
        }
    }

    final class PreviewView: UIView {
        private let session = AVCaptureSession()
        private let queue = DispatchQueue(label: "app.sente.qr")

        override class var layerClass: AnyClass { AVCaptureVideoPreviewLayer.self }
        private var previewLayer: AVCaptureVideoPreviewLayer { layer as! AVCaptureVideoPreviewLayer }

        func start(delegate: AVCaptureMetadataOutputObjectsDelegate) {
            guard let device = AVCaptureDevice.default(for: .video),
                  let input = try? AVCaptureDeviceInput(device: device), session.canAddInput(input) else { return }
            session.addInput(input)
            let output = AVCaptureMetadataOutput()
            guard session.canAddOutput(output) else { return }
            session.addOutput(output)
            output.setMetadataObjectsDelegate(delegate, queue: .main)
            output.metadataObjectTypes = [.qr]
            previewLayer.session = session
            previewLayer.videoGravity = .resizeAspectFill
            // Starting the session blocks for a moment; never on the main thread.
            queue.async { [session] in session.startRunning() }
        }

        func stop() { queue.async { [session] in session.stopRunning() } }
    }
}
