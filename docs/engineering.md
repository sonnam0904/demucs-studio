# Ghi chú kỹ thuật

Các quyết định kỹ thuật đáng biết và cấu trúc code. Phần lớn được ghi lại vì
chúng là nguyên nhân của bug thật, không phải sở thích cá nhân.

---

## Những chỗ dễ sai

**`TORCH_FORCE_NO_WEIGHTS_ONLY_LOAD=1` khi chạy demucs.** PyTorch 2.6 đổi mặc
định `torch.load(weights_only=...)` thành `True`, còn demucs 4.0.1 vẫn pickle cả
object `HTDemucs` chứ không phải state dict — nên trên torch mới, `demucs -n
htdemucs_ft` chết với `UnpicklingError: Unsupported global`. App chỉ bật cờ này
*sau khi* đã verify SHA‑256 của checkpoint.

**Không tin `torch.cuda.is_available()`.** Một wheel PyTorch chỉ chứa kernel cho
các compute capability nó được build cho. Card cũ (ví dụ GTX 960, `sm_52`, với
wheel cu13x) báo `is_available() == True` rồi chết giữa lúc tách với `no kernel
image is available for execution on the device`. App kiểm tra `sm_XX` có nằm
trong `torch.cuda.get_arch_list()` **và** chạy thử một kernel thật; nếu không
được thì báo lý do cụ thể trên badge và tự dùng CPU.

**Đọc progress bar của Python.** `tqdm` vẽ lại bằng ký tự `\r` và không xuống
dòng cho tới khi xong, nên `bufio.ScanLines` sẽ im lặng hàng phút. `internal/proc`
dùng split function coi cả `\r` là dấu kết thúc dòng.

**Huỷ phải diệt cả cây process.** Demucs sinh worker process; kill riêng process
cha để lại chúng ngốn CPU. Trên Unix app đặt process group rồi `kill(-pgid)`;
trên Windows gọi `taskkill /T /F`.

**Cửa sổ trắng trên Linux + NVIDIA.** Renderer DMA‑BUF của WebKitGTK xin GBM
buffer qua thiết bị DRM; với driver NVIDIA proprietary lệnh đó thường trả
`Permission denied`, cửa sổ mở ra với đúng tiêu đề nhưng nội dung trắng hoàn
toàn. App tự đặt `WEBKIT_DISABLE_DMABUF_RENDERER=1` **chỉ khi** phát hiện driver
NVIDIA đang load, và không ghi đè nếu người dùng đã tự set biến này.

**Nghe thử stem: phải qua HTTP loopback, không phải asset server.** Webview
không load được `file://` từ origin `wails://`, nhưng phục vụ qua asset server
cũng **không phát được**: media pipeline của WebKitGTK không chơi resource đến
từ custom URI scheme. Đo trực tiếp trên app này — `fetch()` một file WAV qua
`wails://` trả về 200 với đủ 41 MB body, nhưng gán đúng URL đó cho `<audio>` thì
lỗi `MEDIA_ERR_SRC_NOT_SUPPORTED` (code 4) ở `readyState 0`. Cùng file qua
`http://127.0.0.1` đạt `readyState 4` với duration đúng.

Nên `media.go` mở một listener HTTP trên `127.0.0.1` cổng ngẫu nhiên. Blob URL
dựng từ `fetch()` cũng chạy, nhưng phải nạp trọn file vào RAM — không dùng được
ở đây vì một job có thể sinh 6 stem WAV vài trăm MB. Cổng loopback thì process
nào trên máy cũng nối được, nên có hai lớp chặn: một token random mỗi lần chạy
mà mọi request phải mang, và allow‑list chỉ gồm file app tạo ra hoặc người dùng
tự chọn. `MediaURL` **không** tự thêm vào allow‑list — nếu có, webview sẽ biến
endpoint này thành trình đọc file tuỳ ý.

---

## Cấu trúc

```
main.go                        điểm vào Wails
app.go                         API bind sang TypeScript
media.go                       HTTP loopback phục vụ audio: token + allow-list
webkit_linux.go                workaround WebKitGTK + driver NVIDIA
internal/
  paths/                       mọi đường dẫn on-disk
  settings/                    cấu hình JSON, có normalize + clamp
  proc/                        chạy CLI ngoài: stream \r, huỷ theo cây process
  bus/                         log + progress event gửi lên webview
  netfetch/                    tải HTTP: progress, sha256, kiểm Content-Length
  deps/                        dò tìm & tự cài phụ thuộc, probe GPU
  selfupdate/                  kiểm tra GitHub Release, tự thay thế & khởi động lại
  ytdl/                        yt-dlp → WAV
  engine/
    engine.go                  interface Backend, parser tqdm, ResetOutputDir
    demucs/                    local model repo + demucs CLI
    roformer/                  audio-separator CLI, catalog curated + live
frontend/
  index.html  src/main.ts  src/style.css  src/types.ts
packaging/linux/               nfpm.yaml, .desktop, run.sh
packaging/windows/README.md    hướng dẫn đi kèm bản zip
packaging/macos/README.md      hướng dẫn đi kèm bản dmg
scripts/release-build.sh       build đủ 4 gói cho một version cụ thể
.github/                       workflow build (mọi PR) và release (semantic-release)
```

Test nằm cạnh code (`*_test.go`), trừ `pipeline_test.go` ở gốc — đó là test tích
hợp thật, gate bằng `DEMUCS_STUDIO_E2E=1` vì nó gọi mạng và chạy tách nhạc.

---

## Tự cập nhật { #tu-cap-nhat }

Mỗi lần mở app, `CheckUpdate` hỏi
`api.github.com/repos/…/releases/latest` một lần rồi so version với
`main.appVersion` (giá trị do `-ldflags -X` nhúng lúc build). Lỗi mạng ở bước này
bị nuốt im lặng — app chạy offline được nên "không có mạng" là trạng thái bình
thường, không phải sự cố đáng báo.

**Bản dev không bao giờ được mời cập nhật.** `appVersion` mặc định là `"dev"`,
không parse được thành `x.y.z`, và `Check` thoát ngay trước cả khi gọi mạng. Nếu
không có chặn này thì `make linux` rồi bấm Cập nhật sẽ ghi đè cây làm việc bằng
một bản release.

### Ba backend tính toán { #backend }

`GPU.Backend` là thứ probe **đã xác minh chạy được**, và cũng chính là chuỗi
device truyền cho engine. Vì thế `Thiết bị = Tự động` phải resolve theo
`gpu.Backend`, không được hardcode `"cuda"`.

| Backend | Nền tảng | Bản torch |
| --- | --- | --- |
| `cuda` | Linux, Windows + NVIDIA | wheel theo index `cuXXX` |
| `mps` | macOS + Apple Silicon | wheel macOS duy nhất — **đã có Metal** |
| `cpu` | mọi nơi | |

Điểm khác biệt cốt lõi giữa CUDA và MPS: **trên macOS chỉ có một wheel**. Không
có "bản CPU" và "bản GPU" riêng — `libtorch_cpu.dylib` của wheel darwin arm64
chứa sẵn `MPSGraphTensor`, `MPSStream`, `mps_convolution`. Nên `Accel` là lựa
chọn *cài đặt* trên Linux/Windows nhưng chỉ là *device lúc chạy* trên Mac, và cả
`accel="mps"` lẫn `accel="cpu"` cài về cùng một thứ.

Probe dùng **chung một smoke test** cho cả hai backend (`smoke(device)`): một
kernel elementwise rồi một convolution. Hai thứ đó hỏng độc lập nhau — trên CUDA
convolution đi qua cuDNN, trên MPS nó là op dễ thiếu nhất ở Metal stack cũ.

!!! warning "RoFormer không tôn trọng lựa chọn device trên Mac"

    `audio-separator` không có cờ device: nó tự dò `torch.cuda` rồi
    `torch.backends.mps` và không có env nào ghi đè. `CUDA_VISIBLE_DEVICES=`
    ép được CPU trên NVIDIA nhưng **không ẩn được MPS**. Demucs thì nhận `-d`
    nên tôn trọng lựa chọn.

    Cảnh báo được bắn từ `roformer.cpuCaveat(GOOS, GOARCH)` — keyed theo **máy**,
    không theo backend GPU app đã dò được. `GPU.Backend` chỉ được gán *sau khi*
    smoke test pass, nên gate theo nó sẽ im lặng đúng ở ca cần nói nhất: Mac có
    MPS hỏng, người dùng chọn CPU để né, mà engine vẫn dùng MPS.

### Một luật, mọi nơi cưỡng chế

`settings.AccelApplies(goos, goarch, accel)` là **định nghĩa duy nhất** của
"flavour này cài được trên nền tảng kia không". Mọi chỗ cần biết điều đó đều
**gọi nó**, không chỗ nào chép lại:

| Nơi | Dùng để | Hàm |
| --- | --- | --- |
| Store | chuẩn hoá `settings.json` mang từ máy khác sang | `normalize()` |
| Installer | từ chối giá trị đến từ UI, kèm lời nhắn | `accelUnavailable()` |
| Installer | dựng câu "hãy chọn X hoặc Y" trong mọi lỗi | `suggestAccels()` |
| Danh sách CUDA | có index nào để chọn không | `cudaTargetsForOS()` |
| Gợi ý ban đầu | preselect flavour nào cho máy này | `SuggestedAccel()` |
| RoFormer | máy này có Metal nên CPU không ép được không | `cpuCaveat()` |
| UI | `<option>` nào còn trong dropdown | `Bootstrap.accels`/`.devices` |

Frontend **nhận danh sách qua `Bootstrap`**, không tự suy từ chuỗi `platform`.
Bản trước suy lại ở main.ts và lệch: MPS hiện trên Linux, chọn vào là ghi đè
torch CUDA đang chạy tốt bằng wheel CPU.

Nhãn nút cài đặt sống ở `deps.accelLabels` — **một bản duy nhất**, vì cả câu từ
chối, lỗi "reuse mà không có torch", lẫn test đều cần. Chép tay vào từng chỗ là
cách một câu từ chối trỏ tới cái nút đã bị đổi tên mà mọi test vẫn xanh.

!!! warning "Test so hai bản cài đặt độc lập, không so một hàm với chính nó"

    `accelUnavailable` gọi `AccelApplies` ở dòng đầu, nên so hai thứ đó là so
    một hàm với chính nó: `applies == refused` rút gọn thành `x == !x`, sai với
    mọi input, test không bao giờ đỏ được. `TestAccelPlatformRuleMatchesInstaller`
    vì thế chạy qua **store thật** (`Load` → `Set` → đọc lại) rồi mới so với
    installer — hai đường code khác nhau, nên mới bắt được lúc chúng lệch.

### Cái gì bị thay, và ở đâu

`.deb`/`.rpm` cài binary vào `/usr/bin` do root sở hữu và được trình quản lý gói
theo dõi từng file. Ghi đè sau lưng nó vừa fail vì quyền, vừa làm lệch file list
nếu lỡ thành công. Nên `detectInstall` phân loại trước:

| Kiểu | Dấu hiệu nhận biết | Thứ bị `rename` |
| --- | --- | --- |
| Portable Linux | có `run.sh` cạnh binary | cả thư mục cài |
| Portable Windows | luôn luôn (chỉ phát hành `.zip`) | **riêng file `.exe`** |
| macOS | binary nằm trong `*.app/Contents/MacOS/` | cả bundle `.app` |
| `.deb`/`.rpm` | không có `run.sh` **và** nằm dưới `/usr`, `/opt`… | không thay — UI mở trang release |

Windows là ngoại lệ vì nó **từ chối đổi tên một thư mục đang chứa image đang
chạy**, nhưng lại **cho phép đổi tên chính file image đó**. Đó là lý do duy nhất
`root` trên Windows là file chứ không phải thư mục.

Điều kiện `underSystemPrefix` là có chủ đích: một binary trần nằm ngoài các prefix
hệ thống (output của `go build` chẳng hạn) **không** phải bản `.deb`, nên nó được
xếp `kindUnknown` thay vì hiện thông báo bảo người dùng đi cài lại một gói không
tồn tại.

### Vì sao tráo bằng rename chứ không ghi đè

`Apply` giải nén vào thư mục tạm **nằm trong chính thư mục cha** của bản cài, rồi
đổi tên hai lần:

```
root      → root.old      (bản đang chạy, giữ lại)
staged    → root          (bản mới)
```

Hai điều này chỉ đúng khi tạm nằm cùng filesystem — `os.Rename` không vượt được
mount, và `/tmp` thường là mount khác. Đổi lại:

- Update bị ngắt giữa chừng để lại **hoặc** bản cũ **hoặc** bản mới, không bao giờ
  là hỗn hợp nửa vời.
- Bước hai fail thì đổi tên ngược lại được, người dùng vẫn còn app để mở.
- Tiến trình đang chạy vẫn thực thi bình thường từ `root.old` — trên POSIX
  inode còn sống sau khi đổi tên. `CleanupOld` xoá nó ở lần khởi động sau, vì đó
  là thời điểm đầu tiên nó không còn là image đang chạy.

`stage` còn kiểm binary có thật trong gói tải về trước khi tráo; thiếu bước này
thì một archive hỏng sẽ được cài đè và người dùng mất luôn thứ để mở.

!!! warning "Mới kiểm chứng trên Linux"

    Đường Windows và macOS đã có unit test cho phần phân loại và giải nén, nhưng
    thao tác tráo + khởi động lại chưa chạy thật trên hai OS đó.
