# Build và release

Dành cho người phát triển. Cách dùng ứng dụng ở [docs/usage.md](usage.md).

---

## Build

```bash
make doctor      # kiểm tra phụ thuộc build của Wails
make dev         # dev có live reload
make linux       # binary Linux
make windows     # .exe (cross-compile ngay trên Linux)
make darwin      # .app universal            (chỉ chạy được trên macOS)
make package     # tar.gz + deb + rpm + zip vào dist/
make package-darwin  # .dmg vào dist/        (chỉ chạy được trên macOS)
make test        # unit test
make e2e         # test tích hợp thật (mạng + GPU/CPU)
```

### Linux

Cần `libwebkit2gtk-4.1-dev` và `libgtk-3-dev`. Trên Ubuntu/Debian/Pop!_OS 24.04+
chỉ có webkit2gtk **4.1**, nên mọi lệnh build Linux đều phải có build tag
`webkit2_41` (Makefile đã thêm sẵn).

```bash
sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev
```

### Windows (cross-compile từ Linux)

Chạy được vì backend Windows của Wails dùng WebView2 loader viết bằng Go thuần,
không cần CGO:

```bash
CGO_ENABLED=0 wails build -platform windows/amd64 -skipbindings
```

Muốn ra installer NSIS thì cần `makensis`:

```bash
sudo apt install nsis
make windows-installer
```

Máy Windows của người dùng cần **WebView2 Runtime** — Windows 11 có sẵn,
Windows 10 thì installer NSIS của Wails tự tải về.

> Chiều ngược lại (build bản Linux trên Windows) thì không được: bản Linux cần
> CGO liên kết với webkit2gtk.

### macOS (bắt buộc build trên máy Mac)

Không cross-compile được từ Linux: backend macOS của Wails dùng WKWebView, cần
CGO liên kết với framework hệ thống. Trên máy Mac chỉ cần Go, Node và Xcode
Command Line Tools:

```bash
make package-darwin VERSION=1.4.0   # → dist/demucs-studio-1.4.0-macos-universal.dmg
```

`make darwin` build `-platform darwin/universal`, tức Wails compile cả amd64 lẫn
arm64 rồi `lipo` lại thành một binary — nên chỉ có **một** file `.dmg` cho cả
Intel và Apple Silicon.

Gói `.dmg` dựng bằng `hdiutil` và `ditto`, cả hai đều có sẵn trong macOS. Chủ ý
không dùng `create-dmg`: nó kéo thêm phụ thuộc Homebrew và một bước AppleScript
để trang trí cửa sổ, vốn hay flaky trên CI runner.

!!! warning "App không được ký bằng Developer ID"

    Bundle chỉ được **ad-hoc sign** (`codesign --sign -`). Việc này là bắt buộc:
    Apple Silicon từ chối nạp binary arm64 hoàn toàn không có chữ ký — tiến trình
    bị SIGKILL trước khi vào `main`. Nhưng ad-hoc **không** qua được Gatekeeper,
    nên người dùng tải `.dmg` về vẫn phải tự gỡ cờ quarantine
    (`packaging/macos/README.md` hướng dẫn sẵn, và file này được copy vào trong
    `.dmg`).

    Muốn hết cảnh báo thì cần tài khoản Apple Developer Program và bước
    notarize — chưa làm.

### ffmpeg trên macOS

`yt-dlp/FFmpeg-Builds` — nguồn app dùng cho Linux và Windows — **không publish
asset macOS nào**. Nên trên darwin, `internal/deps/install.go` tải từ
[`eugeneware/ffmpeg-static`](https://github.com/eugeneware/ffmpeg-static) thay
thế. Khác biệt kéo theo:

- Hai file tải riêng (`ffmpeg-darwin-*` và `ffprobe-darwin-*`) thay vì một
  archive chứa cả hai.
- Là **binary trần**, không phải `.zip`/`.tar.xz`, nên không qua bước giải nén.
- Dự án đó không công bố manifest checksum, nên chỉ còn kiểm `Content-Length`
  của `netfetch`.
- Tên asset dùng chuỗi kiến trúc của Node: `amd64` của Go là `x64`.

## Icon

Nguồn duy nhất là **`build/appicon.png`** — ảnh vuông, nên là 1024×1024, PNG có
nền trong suốt nếu icon bo góc.

Thay file đó rồi chạy:

```bash
make icons
```

Lệnh này sinh lại và **các file sinh ra cần commit**:

| File | Dùng ở đâu |
| --- | --- |
| `build/windows/icon.ico` | Icon của `.exe` và của installer NSIS |
| `frontend/public/appicon.png` | Header trong app và favicon của webview |
| `docs/assets/appicon.png` | Logo và favicon của site tài liệu |

`build/appicon.png` còn được dùng trực tiếp ở ba nơi khác, không cần sinh lại:
`main.go` nhúng nó làm icon cửa sổ Linux, `nfpm.yaml` cài nó vào
`hicolor/512x512`, và README hiển thị nó ở đầu trang.

Cần Pillow (`pip install pillow`). Chủ ý không gắn vào `make linux` / `make
windows`: đây là việc hiếm, và file sinh ra đã nằm trong repo.

---

## Release

Version do **semantic-release** quyết định từ commit message, không sửa tay ở
đâu cả. Push lên `master` là `.github/workflows/release.yml` chạy: tính version
→ build đủ 5 gói → tạo GitHub Release kèm artifact → commit ngược `CHANGELOG.md`
và `wails.json` (có `[skip ci]` nên không lặp).

Commit phải theo [Conventional Commits](https://www.conventionalcommits.org):

| Prefix | Bump | Ví dụ |
| --- | --- | --- |
| `fix:` | patch | `fix(roformer): tách sai khi tên file có dấu` |
| `feat:` | minor | `feat(ui): thêm nút huỷ khi đang tải` |
| `feat!:` hoặc footer `BREAKING CHANGE:` | major | `feat!: bỏ engine Demucs v3` |
| `chore:` `docs:` `refactor:` `test:` `ci:` | không release | |

Build diễn ra **bên trong** semantic-release (`prepareCmd`) chứ không phải ở một
workflow riêng nghe tag. Lý do: tag và release tạo bằng `GITHUB_TOKEN` không
trigger workflow khác, nên workflow nghe tag sẽ không bao giờ chạy nếu không
thêm PAT.

### Vì sao release có ba job

Bản macOS không cross-compile được từ Linux, nên `release.yml` tách làm ba:

| Job | Runner | Việc |
| --- | --- | --- |
| `version` | ubuntu | `semantic-release --dry-run` để biết trước version sắp phát hành |
| `macos` | macos-14 | build `.dmg` đúng version đó, upload làm workflow artifact |
| `release` | ubuntu | tải `.dmg` về `dist/`, build Linux + Windows, publish cả 5 asset |

Phải biết version **trước** khi build mac vì số đó được nhúng vào `Info.plist` và
vào tên file. Cách này đổi lại việc release không bao giờ thiếu asset macOS
trong vài phút đầu — khác với phương án upload bổ sung sau khi đã tạo release.

Hai lần chạy semantic-release (dry-run và thật) thấy cùng tập commit vì
`concurrency: release` không cho hai release chồng nhau. Nếu vì lý do nào đó
chúng vẫn lệch, `release-build.sh` đối chiếu tên file `.dmg` với version nó được
truyền và **fail** thay vì publish asset lệch version.

`.github/workflows/build.yml` build đúng những gói đó nhưng **không publish** —
chạy trên mọi pull request, và dispatch tay được để lấy bản build của nhánh bất
kỳ. Nó cũng tách job Linux/Windows và job macOS như trên. Mọi job dùng chung
composite action `.github/actions/setup-build`, action này tự bỏ qua bước `apt`
và `nfpm` khi chạy trên macOS.

Build một version cụ thể ở máy local:

```bash
scripts/release-build.sh 1.4.0          # → dist/*.tar.gz .deb .rpm .zip
scripts/release-build-macos.sh 1.4.0    # → dist/*.dmg          (trên máy Mac)
```

`release-build.sh` dọn archive cũ trong `dist/` trước khi build, và **fail nếu
thiếu bất kỳ gói nào** — cần thiết vì target `deb` trong Makefile thoát 0 khi
không có `nfpm`, nếu không release sẽ âm thầm thiếu `.deb`/`.rpm`. Bước dọn cố ý
không đụng tới `.dmg`, vì file đó do job khác đặt vào. Đặt `EXPECT_MACOS=1` để
script đòi luôn cả `.dmg` — release job bật cờ này.

---

## Tài liệu

Site này dựng bằng [MkDocs Material](https://squidfunk.github.io/mkdocs-material/)
từ thư mục `docs/`. Xem thử ở máy, có live reload:

```bash
pip install -r docs/requirements.txt
mkdocs serve          # http://127.0.0.1:8000
```

`.github/workflows/docs.yml` build và đẩy lên GitHub Pages mỗi khi `docs/` hoặc
`mkdocs.yml` đổi trên `master`. Pull request thì chỉ build, không deploy.

Build chạy ở chế độ `--strict` nên **link hỏng hoặc `#anchor` không tồn tại sẽ
làm fail**, thay vì lặng lẽ lên site.

!!! warning "Slugify tiếng Việt nuốt mất chữ `đ`"

    Anchor tự sinh dùng NFKD rồi bỏ ký tự non-ASCII. `đ` (U+0111) không tách được
    thành `d` + dấu nên bị **xoá hẳn**: heading *"Ứng dụng tìm công cụ ở đâu"* ra
    anchor `#ung-dung-tim-cong-cu-o-au` — chú ý `o-au`, không phải `o-dau`.

    Nên với heading nào được link tới từ trang khác, hãy gắn anchor tường minh:

    ```markdown
    ## Thư mục dữ liệu { #thu-muc-du-lieu }
    ```
