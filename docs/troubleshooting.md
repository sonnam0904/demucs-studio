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
bạn. Nhưng nếu **không có cái nào** thì hãy cài một cái — `deno` nhẹ nhất:

=== "Linux"

    ```bash
    curl -fsSL https://deno.land/install.sh | sh
    ```

=== "Windows"

    ```powershell
    winget install DenoLand.Deno
    ```

Cài xong bấm **Kiểm tra lại** ở tab **Phụ thuộc**.

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
một lần tách chạy giữa đường rồi chết. Muốn dùng GPU thì cài lại engine với phiên
bản CUDA cũ hơn (thử `cu118` hoặc `cu121` ở ô **CUDA**).

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

## Kết quả tách có lẫn file của lần chạy trước

Không còn xảy ra: thư mục `stems/<tên bài>/<model>/` được dọn sạch trước mỗi lần
chạy. Nghĩa là **tách lại cùng một bài bằng cùng một model sẽ ghi đè kết quả cũ** —
muốn giữ thì copy ra ngoài trước. Kết quả của **model khác** trên cùng bài vẫn còn.

---

Vẫn chưa xong? [Mở issue](https://github.com/sonnam0904/demucs-studio/issues) và
kèm theo nội dung ô **Nhật ký** — đó là thứ hữu ích nhất để tìm nguyên nhân.
