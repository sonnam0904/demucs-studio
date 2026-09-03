# Xử lý sự cố

Ô **Nhật ký** ở đáy cửa sổ chứa nguyên văn output của yt-dlp, demucs và
audio-separator. Mở nó ra trước khi tìm lỗi ở đây — thường câu trả lời đã nằm sẵn
trong đó.

---

## Tải nhạc báo `HTTP Error 403: Forbidden` { #403 }

Đây là lỗi phổ biến nhất, và gần như luôn cùng một nguyên nhân: **yt-dlp đã cũ**.
YouTube ra cơ chế chống bot mới vài tuần một lần, và một yt-dlp cũ hơn cơ chế đó
vẫn lấy được danh sách format nhưng bị chặn khi tải.

**Cách sửa:** tab **Phụ thuộc** → **Cập nhật yt-dlp**.

??? note "Đã kiểm chứng thế nào"

    Một video lỗi 403 với yt-dlp `2026.07.04` tải bình thường ngay với
    `2026.08.19`, không đổi gì khác.

    Nếu yt-dlp trên máy cài bằng `pip` thì lệnh `yt-dlp --update` sẽ từ chối tự
    cập nhật. App phát hiện chuyện đó và tải bản standalone vào `<AppDir>/bin` —
    thư mục này được ưu tiên hơn `PATH` nên bản mới thắng, bạn không phải sửa gì
    thủ công.

## Nhật ký ghi *"No supported JavaScript runtime could be found"*

YouTube bắt giải một thử thách JavaScript để lấy link media. yt-dlp chỉ tự bật
`deno`; nếu máy chỉ có `node` hoặc `bun` thì nó báo dòng trên, coi đường extraction
đó là **deprecated**, và mất một số format.

App tự dò `deno` / `node` / `bun` rồi truyền `--js-runtimes <tên>:<đường dẫn>` giúp
bạn. Nhưng phải có ít nhất một cái trên máy.

**Cách sửa:** tab **Phụ thuộc** → dòng **JS runtime** → **Cài tự động**. App tải
`deno` (~90 MB) vào thư mục riêng của nó, không đụng tới hệ thống.

??? note "Muốn tự cài thay vì để app tải"

    Cài `deno`, `node` hoặc `bun` theo cách thông thường rồi bấm **Kiểm tra lại**:

    === "Linux / macOS"

        ```bash
        curl -fsSL https://deno.land/install.sh | sh
        ```

    === "Windows"

        ```powershell
        winget install DenoLand.Deno
        ```

    App ưu tiên bản trong thư mục của nó hơn bản trong `PATH`, nên nếu đã lỡ bấm
    *Cài tự động* thì bản đó sẽ được dùng. Xoá
    `<thư mục dữ liệu>/bin/deno` để quay lại bản hệ thống.

## Video bị chặn hoặc giới hạn tuổi

Vào **Cấu hình** → *Cookies từ browser* → chọn browser đang đăng nhập YouTube.
yt-dlp sẽ đọc cookie từ browser đó và tải như một người đã đăng nhập.

Browser phải **đóng** khi tải, vì một số browser giữ khoá cơ sở dữ liệu cookie.

## Badge trên cùng ghi *CPU only* dù máy có GPU NVIDIA { #gpu }

Trỏ chuột vào badge để xem lý do cụ thể. Hai trường hợp hay gặp:

| Lý do trên badge | Nghĩa là gì |
| --- | --- |
| *chưa tìm thấy PyTorch* | Chưa cài engine, hoặc cài bản CPU. Cài lại ở tab **Phụ thuộc** với PyTorch = *Tải bản CUDA* |
| *GPU … không nằm trong các kiến trúc torch hỗ trợ* | Bản PyTorch không có kernel biên dịch cho dòng card của bạn |

Trường hợp thứ hai xảy ra với card cũ: một wheel PyTorch chỉ chứa kernel cho các
compute capability nó được build cho. Ví dụ GTX 960 là `sm_52`, còn wheel `cu13x`
chỉ có từ `sm_75` trở lên.

App phát hiện việc này **trước khi** tách, nên bạn thấy badge *CPU only* thay vì
một lần tách chạy giữa đường rồi chết. Đáng chú ý: `torch.cuda.is_available()`
vẫn trả về `True` trong trường hợp này — driver hoạt động, chỉ là không có mã máy
nào chạy được. Vì thế app phóng thử một kernel thật chứ không tin vào cờ đó.

Cách chữa: tab **Phụ thuộc** → PyTorch *Tải bản CUDA (GPU)*. Ô **CUDA** liệt kê
các dòng card mỗi bản hỗ trợ và **đánh dấu bản phù hợp với máy bạn**, nên chỉ
việc chọn cái được đánh dấu rồi *Cài engine*.

Bảng arch dưới đây đọc trực tiếp từ wheel bằng `cuobjdump --list-elf`:

| Index | torch | Compute capability có kernel |
| --- | --- | --- |
| `cu118` | 2.7.1 | `sm_37 50 60 70 75 80 86 90` |
| `cu126` | 2.14 | `sm_50 60 70 75 80 86 89 90` |
| `cu130` | 2.14 | `sm_75 80 86 90 100 120` |

Chỉ `cu130` bỏ Maxwell/Pascal — **`cu126` mặc định vẫn chạy được GTX 9xx/10xx**.
Đã kiểm chứng end-to-end trên GTX 960 (`sm_52`): tách nhạc chạy trên GPU.

!!! info "Vì sao không có `sm_52` trong bảng mà GTX 960 vẫn chạy"

    Cubin `sm_XY` chạy được trên mọi card **cùng major, minor bằng hoặc cao
    hơn** — đúng như cảnh báo của torch: *"5.0 which supports hardware CC
    >=5.0,<6.0"*. Nên `sm_50` phủ cả 5.2. Cùng lý do đó, `sm_86` phủ RTX 40xx
    (8.9) ở những index không có `sm_89`.

!!! note "Lần cài này lâu hơn bình thường"

    Đổi phiên bản PyTorch là **hạ cấp**, mà `pip --upgrade` không hạ cấp được.
    Nên app gỡ cả họ `torch`/`torchaudio`/`torchvision` rồi cài lại, và tạo lại
    môi trường Python nếu kiểu venv hiện tại không phù hợp. Tải lại vài GB.

## Tách chết giữa đường với *GET was unable to find an engine* { #cudnn }

Đây là lỗi **cuDNN**, không phải CUDA. Traceback dừng ở `F.conv1d`, và badge
trước đó vẫn báo GPU sẵn sàng.

Nguyên nhân: các gói `nvidia-cudnn-cu11`, `nvidia-cudnn-cu13`… giải nén vào
**cùng một thư mục** `nvidia/cudnn/lib` và **ghi đè `libcudnn.so.9` của nhau**.
Nên khi đổi phiên bản CUDA mà gói cuDNN của bản cũ còn sót lại, torch nạp đúng
file sai:

```
torch 2.7.1+cu118   cần cuDNN cu11 9.1.0
torch.backends.cudnn.version()  →  92400   ← của cu13, sai
```

Phép cộng elementwise vẫn chạy vì nó **không đi qua cuDNN** — chỉ có convolution
mới chết, tức là chết đúng lúc đang tách.

Bản mới xử lý cả hai mặt: khi đổi phiên bản PyTorch, app gỡ **toàn bộ** runtime
CUDA (`nvidia-*`, `cuda-*`) chứ không chỉ mấy gói `torch*`; và probe GPU chạy
thêm **một convolution thật** nên phát hiện được trước khi bạn bấm Tách.

Nếu đang mắc, sửa tay:

```bash
PY=~/.local/share/demucs-studio/pyenv/bin/python
$PY -m pip list | grep -i cudnn        # xem có mấy bản
$PY -c "import torch; print(torch.backends.cudnn.version())"
```

Có nhiều hơn một gói `nvidia-cudnn-*` thì cài lại engine để app dọn sạch.

## GPU báo hết VRAM { #vram }

**Cấu hình** → *Tham số RoFormer*:

- giảm **Segment size**: 256 → 128 → 64
- giữ **Batch size** = 1

Với Demucs, đặt **Segment** = 5–7 thay vì 0.

Không đủ VRAM thật thì chọn **Thiết bị** = *CPU* và dùng `htdemucs_ft` — xem
[so sánh tốc độ](usage.md#chon-model).

## Tách rất chậm

Xem badge trên cùng. Nếu là *CPU only* thì đó là nguyên nhân, và RoFormer trên CPU
chậm gấp nhiều lần Demucs. Chuyển sang `htdemucs_ft`.

Đang chạy GPU mà vẫn chậm: đặt **Shifts** = 0 (mỗi đơn vị shift nhân thời gian
chạy lên đúng hệ số đó), và kiểm tra không có tiến trình nào khác đang chiếm GPU.

## Cửa sổ mở ra nhưng trắng trơn (Linux + NVIDIA)

Renderer DMA‑BUF của WebKitGTK xin GBM buffer qua thiết bị DRM, và với driver
NVIDIA proprietary lệnh đó thường trả `Permission denied` — cửa sổ hiện đúng tiêu
đề nhưng nội dung trắng hoàn toàn.

App tự đặt `WEBKIT_DISABLE_DMABUF_RENDERER=1` khi phát hiện driver NVIDIA đang
load, nên bình thường bạn không gặp. Nếu vẫn trắng, chạy tay:

```bash
WEBKIT_DISABLE_DMABUF_RENDERER=1 demucs-studio
```

## Player không phát được, chỉ hiện *Error*

Kiểm tra thư viện GStreamer — WebKitGTK giải mã audio qua nó:

```bash
sudo apt install gstreamer1.0-plugins-base gstreamer1.0-plugins-good
```

## Bấm *Cài engine* nhưng không xong

Xem ô **Nhật ký** — toàn bộ output của `pip` hiện ở đó. Vài nguyên nhân hay gặp:

| Hiện tượng trong nhật ký | Cách xử lý |
| --- | --- |
| `không tìm thấy Python 3` | Cài Python 3.10+ và đảm bảo nó nằm trong `PATH`. Trên Windows dùng bản python.org, không dùng bản Microsoft Store |
| Treo rất lâu ở bước tải torch | Bình thường — bản CUDA nặng khoảng 2,5 GB |
| `No space left on device` | Môi trường Python đầy đủ chiếm khoảng 3–5 GB trong thư mục dữ liệu |

Muốn làm lại từ đầu: xoá thư mục `pyenv/` trong
[thư mục dữ liệu](installation.md#thu-muc-du-lieu) rồi cài lại.

## Bấm *Mở thư mục kết quả* không thấy gì (Windows)

Xảy ra với bản **1.1.0 và cũ hơn**. App gọi
`rundll32 url.dll,FileProtocolHandler` để mở thư mục; entry point đó nhận nguyên
phần còn lại của dòng lệnh **kèm cả dấu nháy**, mà mọi thư mục kết quả đều có
dấu cách trong tên bài hát nên Go phải nháy nó lại — đường dẫn tới tay Windows ở
dạng không dùng được.

Tệ hơn: app chỉ kiểm tra tiến trình con **khởi động** được hay không, nên khi nó
thất bại sau đó thì cú bấm vừa không mở gì vừa không báo gì.

Bản mới dùng `explorer.exe` (cách Microsoft khuyến nghị cho việc mở thư mục, và
nó nhận tham số như một chương trình bình thường), đồng thời ghi vào **Nhật ký**
lệnh đã chạy để lần sau còn chẩn đoán được.

Chưa cập nhật được thì mở tay: thư mục kết quả nằm ở
`%USERPROFILE%\Music\DemucsStudio\stems\<tên bài>\<model>\`.

## Kết quả tách có lẫn file của lần chạy trước

Không còn xảy ra: thư mục `stems/<tên bài>/<model>/` được dọn sạch trước mỗi lần
chạy. Nghĩa là **tách lại cùng một bài bằng cùng một model sẽ ghi đè kết quả cũ** —
muốn giữ thì copy ra ngoài trước. Kết quả của **model khác** trên cùng bài vẫn còn.

---

Vẫn chưa xong? [Mở issue](https://github.com/sonnam0904/demucs-studio/issues) và
kèm theo nội dung ô **Nhật ký** — đó là thứ hữu ích nhất để tìm nguyên nhân.
