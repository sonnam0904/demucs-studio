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
  ytdl/                        yt-dlp → WAV
  engine/
    engine.go                  interface Backend, parser tqdm, ResetOutputDir
    demucs/                    local model repo + demucs CLI
    roformer/                  audio-separator CLI, catalog curated + live
frontend/
  index.html  src/main.ts  src/style.css  src/types.ts
packaging/linux/               nfpm.yaml, .desktop, run.sh
packaging/windows/README.md    hướng dẫn đi kèm bản zip
scripts/release-build.sh       build đủ 4 gói cho một version cụ thể
.github/                       workflow build (mọi PR) và release (semantic-release)
```

Test nằm cạnh code (`*_test.go`), trừ `pipeline_test.go` ở gốc — đó là test tích
hợp thật, gate bằng `DEMUCS_STUDIO_E2E=1` vì nó gọi mạng và chạy tách nhạc.
