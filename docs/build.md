# Build từ source

Dành cho người phát triển. Cách dùng ứng dụng ở [Hướng dẫn sử dụng](usage.md).

Cần **Go** và **Node.js**, cộng thêm vài thứ tuỳ nền tảng ở các mục bên dưới.
Kiểm tra nhanh xem đã đủ chưa:

```bash
make doctor
```

---

## Các lệnh

| Lệnh | Kết quả |
| --- | --- |
| `make dev` | Chạy app ở chế độ dev, có live reload |
| `make linux` | Binary Linux → `build/bin/demucs-studio` |
| `make windows` | `.exe` → `build/bin/demucs-studio.exe` |
| `make darwin` | `.app` universal *(chỉ chạy được trên macOS)* |
| `make package` | `.tar.gz` + `.deb` + `.rpm` + `.zip` vào `dist/` |
| `make package-darwin` | `.dmg` vào `dist/` *(chỉ chạy được trên macOS)* |
| `make test` | Unit test |
| `make e2e` | Test tích hợp thật — có gọi mạng và chạy tách nhạc thật |

Các target `package*` nhận biến `VERSION`, mặc định là `0.1.0`:

```bash
make package VERSION=1.4.0
```

---

## Linux

Cần `libwebkit2gtk-4.1-dev` và `libgtk-3-dev`:

```bash
sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev
```

Trên Ubuntu/Debian/Pop!_OS 24.04+ chỉ có webkit2gtk **4.1**, nên mọi lệnh build
Linux đều phải kèm build tag `webkit2_41` — Makefile đã thêm sẵn.

`make package` còn cần [`nfpm`](https://github.com/goreleaser/nfpm) để dựng
`.deb` và `.rpm`:

```bash
go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest
```

!!! warning "Thiếu `nfpm` thì không báo lỗi"

    Target `deb` thoát 0 khi không tìm thấy `nfpm`, nên `make package` vẫn báo
    thành công nhưng `dist/` chỉ có `.tar.gz` và `.zip`.

## Windows — cross-compile từ Linux

Chạy được ngay trên Linux vì backend Windows của Wails dùng WebView2 loader viết
bằng Go thuần, không cần CGO:

```bash
make windows
```

Muốn ra installer NSIS thì cần `makensis`:

```bash
sudo apt install nsis
make windows-installer
```

> Chiều ngược lại (build bản Linux trên Windows) thì không được: bản Linux cần
> CGO liên kết với webkit2gtk.

## macOS — bắt buộc build trên máy Mac

Không cross-compile được từ Linux: backend macOS của Wails dùng WKWebView, cần
CGO liên kết với framework hệ thống. Trên máy Mac chỉ cần Go, Node và Xcode
Command Line Tools:

```bash
make package-darwin VERSION=1.4.0
# → dist/demucs-studio-1.4.0-macos-universal.dmg
```

`make darwin` build `-platform darwin/universal`, tức Wails compile cả amd64 lẫn
arm64 rồi `lipo` lại thành một binary — nên chỉ có **một** file `.dmg` dùng
được cho cả Intel và Apple Silicon.

!!! note "Bundle chỉ được ad-hoc sign"

    `make package-darwin` chạy `codesign --sign -`. Bước này bắt buộc vì Apple
    Silicon từ chối nạp binary arm64 hoàn toàn không có chữ ký. Nhưng ad-hoc
    **không** qua được Gatekeeper, nên bản `.dmg` build ra vẫn cần gỡ cờ
    quarantine trước khi mở — `packaging/macos/README.md` hướng dẫn sẵn và được
    copy vào trong `.dmg`.
