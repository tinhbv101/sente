import AVFoundation
import PhotosUI
import SwiftUI
import Vision
import SenteUI

/// The one scanner. A Sente QR code is either an invitation or a friend code,
/// and only the link's path says which -- both codes are eight characters from
/// the same alphabet. Classification happens here; acting on it belongs to the
/// router, so this screen never performs a write of its own.
struct ScanView: View {
    let onScanned: (InviteCode.Scanned) -> Void
    @Environment(\.dismiss) private var dismiss
    @Environment(AppSession.self) private var session

    @State private var permission = AVCaptureDevice.authorizationStatus(for: .video)
    @State private var handled = false
    @State private var note: String?
    @State private var picked: PhotosPickerItem?
    @State private var reading = false

    private var hasCamera: Bool { AVCaptureDevice.default(for: .video) != nil }

    var body: some View {
        NavigationStack {
            ZStack {
                Tokens.ink.ignoresSafeArea()
                camera
                VStack {
                    Spacer()
                    aim
                    Spacer()
                    footer
                }
            }
            .navigationTitle("Quét mã QR")
            .navigationBarTitleDisplayMode(.inline)
            .toolbarColorScheme(.dark, for: .navigationBar)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Đóng") { dismiss() } } }
        }
        .onChange(of: picked) { _, item in if let item { Task { await read(item) } } }
    }

    @ViewBuilder private var camera: some View {
        // A device with no camera must never be asked for permission it cannot
        // honour: it goes straight to picking a photo instead.
        if !hasCamera {
            message(LS(localized: "Máy này không có camera. Bạn vẫn có thể chọn ảnh có mã QR."),
                    systemImage: "camera.fill")
        } else {
            switch permission {
            case .authorized:
                QRScannerView { payload in handle(payload) }
                    .ignoresSafeArea()
            case .denied, .restricted:
                message(LS(localized: "Sente chưa được phép dùng camera."), systemImage: "camera.badge.ellipsis") {
                    Button("Mở Cài đặt") {
                        if let url = URL(string: UIApplication.openSettingsURLString) { UIApplication.shared.open(url) }
                    }
                    .buttonStyle(PrimaryButton())
                }
            default:
                ProgressView().tint(.white)
                    .task {
                        let granted = await AVCaptureDevice.requestAccess(for: .video)
                        permission = granted ? .authorized : .denied
                    }
            }
        }
    }

    @ViewBuilder private var aim: some View {
        if hasCamera, permission == .authorized {
            VStack(spacing: 14) {
                RoundedRectangle(cornerRadius: 24)
                    .strokeBorder(.white.opacity(0.9), lineWidth: 3)
                    .frame(width: 240, height: 240)
                Text("Quét mã lời mời để vào ván, hoặc mã bạn bè để kết bạn.")
                    .font(.footnote.weight(.medium)).foregroundStyle(.white)
                    .multilineTextAlignment(.center).padding(.horizontal, 40)
            }
        }
    }

    private var footer: some View {
        VStack(spacing: 12) {
            if let note {
                Text(note)
                    .font(.footnote.weight(.semibold)).foregroundStyle(.white)
                    .multilineTextAlignment(.center)
                    .padding(.horizontal, 14).padding(.vertical, 10)
                    .background(.black.opacity(0.55), in: Capsule())
            }
            PhotosPicker(selection: $picked, matching: .images, photoLibrary: .shared()) {
                if reading {
                    ProgressView().tint(.white).frame(maxWidth: .infinity).frame(height: 50)
                } else {
                    Label("Chọn ảnh có mã QR", systemImage: "photo.on.rectangle")
                        .font(.callout.weight(.semibold))
                        .frame(maxWidth: .infinity).frame(height: 50)
                }
            }
            .background(.white.opacity(0.16), in: RoundedRectangle(cornerRadius: 15))
            .foregroundStyle(.white)
            .disabled(reading)
        }
        .padding(.horizontal, 20).padding(.bottom, 24)
    }

    /// One payload, whatever it came from. `handled` latches only once something
    /// is actually acted on: latching on every payload would make the scanner
    /// deaf to the right code after one stray QR drifted through the frame.
    private func handle(_ payload: String) {
        guard !handled else { return }
        switch InviteCode.classify(payload) {
        case .friend(let code) where code == session.user?.friendCode:
            // The server folds "yourself" into the same 404 as an unknown code,
            // which would read as "my own code is broken".
            note = LS(localized: "Đây là mã của chính bạn.")
        case .foreign, .malformed:
            note = LS(localized: "Mã này không phải của Sente.")
        case let scanned:
            handled = true
            onScanned(scanned)
        }
    }

    /// Reads a QR out of a saved photo. Everything Vision touches is built and
    /// consumed inside the task: its handler is not Sendable, so only Data may
    /// cross the boundary.
    private func read(_ item: PhotosPickerItem) async {
        reading = true
        defer { reading = false; picked = nil }
        guard let data = try? await item.loadTransferable(type: Data.self) else {
            note = LS(localized: "Không đọc được ảnh này.")
            return
        }
        let payloads = await Task.detached(priority: .userInitiated) { () -> [String] in
            let request = VNDetectBarcodesRequest()
            request.symbologies = [.qr]
            let handler = VNImageRequestHandler(data: data, options: [:])
            try? handler.perform([request])
            return (request.results ?? []).compactMap(\.payloadStringValue)
        }.value

        guard !payloads.isEmpty else {
            note = LS(localized: "Ảnh này không có mã QR.")
            return
        }
        for payload in payloads {
            handle(payload)
            if handled { return }
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

/// A QR code on a white card. Every screen that shows one needs the card: the
/// code is drawn in black on transparent, and the app's dark paper is nearly
/// black -- without it the image looks fine and no camera can read it.
struct QRCard: View {
    let payload: String
    var size: CGFloat = 200

    var body: some View {
        Group {
            if let image = QRCode.image(for: payload) {
                Image(uiImage: image)
                    .interpolation(.none)
                    .resizable().scaledToFit()
                    .frame(width: size, height: size)
            } else {
                Color.clear.frame(width: size, height: size)
            }
        }
        .padding(12)
        .background(.white, in: RoundedRectangle(cornerRadius: 16))
    }
}
