# DemucsStudio

Dán link YouTube → tải nhạc về máy → tách riêng **giọng hát** và **nhạc nền**.

Mọi thứ chạy ngay trên máy bạn, không cần tài khoản, không upload file đi đâu cả.

---

## 1. Mở ứng dụng

Nhấp đúp vào **demucs-studio.exe** trong thư mục này.

- Windows hiện cảnh báo "Windows protected your PC"? → bấm **More info** → **Run anyway**.
  (Ứng dụng chưa mua chứng chỉ ký số, nên Windows cảnh báo mặc định.)
- Nếu báo thiếu **WebView2**: tải và cài
  [WebView2 Runtime](https://developer.microsoft.com/microsoft-edge/webview2/)
  rồi mở lại. Windows 11 đã có sẵn, chỉ Windows 10 mới cần bước này.

Bạn có thể copy nguyên thư mục này đi đâu cũng được — kể cả USB.

---

## 2. Cài đặt lần đầu (làm một lần duy nhất)

Mở tab **Phụ thuộc** ở đầu cửa sổ.

### Bước 1 — Công cụ tải nhạc

Bấm **Cài tự động** ở hai dòng `yt-dlp` và `ffmpeg`. Chờ vài chục giây.

Xong bước này là đã **tải nhạc từ YouTube về máy** được rồi.

### Bước 2 — Cài Python (nếu muốn tách vocal)

Tải **Python 3.12** tại [python.org/downloads/windows](https://www.python.org/downloads/windows/)
và cài đặt.

> ⚠️ Trong màn hình cài đặt Python, **nhớ tích ô "Add python.exe to PATH"** ở dưới cùng.
>
> ⚠️ Đừng cài Python từ **Microsoft Store** — bản đó không dùng được.

### Bước 3 — Cài bộ tách nhạc

Vẫn ở tab **Phụ thuộc**, tìm khối **Cài engine Python**:

| Máy bạn | Chọn mục PyTorch |
| --- | --- |
| Có card đồ hoạ **NVIDIA** | **Tải bản CUDA (GPU)** — tách nhanh gấp nhiều lần |
| Không có card NVIDIA | **Tải bản CPU** |

Tích chọn engine muốn dùng rồi bấm **Cài engine**.

Lần cài này tải khá nhiều dữ liệu (bản GPU khoảng **2.5 GB**), nên có thể mất
10–30 phút tuỳ mạng. Cứ để cửa sổ mở, xem tiến độ ở khung **Nhật ký** phía dưới.

Cài xong một lần là dùng mãi.

---

## 3. Tách nhạc

1. Vào tab **Tách nhạc**.
2. Dán link YouTube vào ô đầu tiên → bấm **Tải WAV**.
   Muốn xem trước tên bài, thời lượng thì bấm **Xem thông tin**.
3. Chờ tải xong, bài hát hiện ra kèm ảnh bìa và trình phát để nghe thử.
4. Chọn **Model** (xem bảng gợi ý bên dưới), để **Thiết bị** ở *Tự động*.
5. Bấm **Tách vocal**.
6. Xong sẽ có hai file: giọng hát (`vocals.wav`) và nhạc nền (`no_vocals.wav`),
   nghe thử ngay trong app hoặc bấm **Mở thư mục kết quả**.

Lần đầu dùng một model, app cần tải model đó về (300 MB – 1 GB). Những lần sau
dùng lại ngay, không tải nữa.

### Nên chọn model nào?

| Bạn muốn | Chọn |
| --- | --- |
| Nhanh, máy chỉ có CPU | **Demucs — htdemucs_ft** |
| Chất lượng cao nhất, có GPU NVIDIA | **BS-RoFormer — Vocals Resurrection** |
| Cân bằng, có GPU | **Mel-Band RoFormer — Kim FT2** |

Model RoFormer cho giọng hát sạch hơn hẳn, nhưng **chạy bằng CPU thì rất chậm** —
một đoạn 19 giây mất gần 3 phút, trong khi `htdemucs_ft` chỉ 35 giây. Không có
card NVIDIA thì cứ dùng `htdemucs_ft`.

---

## 4. Gặp vấn đề?

**Không tải được video / báo lỗi lạ khi tải**
→ Tab **Phụ thuộc** → **Cập nhật yt-dlp**. YouTube thay đổi liên tục, đây là
nguyên nhân phổ biến nhất.

**Video bị chặn hoặc giới hạn độ tuổi**
→ Tab **Cấu hình** → **Cookies từ browser** → chọn trình duyệt bạn đang đăng nhập
YouTube.

**Tách nhạc báo hết bộ nhớ GPU (VRAM)**
→ Tab **Cấu hình** → giảm **Segment size** từ 256 xuống 128, giữ **Batch size** = 1.
Hoặc đổi **Thiết bị** sang *CPU*.

**Tách rất chậm dù có card NVIDIA**
→ Có thể bạn đã cài nhầm bản CPU. Vào **Phụ thuộc** → cài lại engine, lần này
chọn **Tải bản CUDA (GPU)**.

**Muốn chất lượng tối đa**
→ Dùng `htdemucs_ft` và tăng **Shifts** lên 2–5 trong Cấu hình. Thời gian chạy
tăng theo đúng số lần đó.

**Muốn thêm model khác**
→ Tab **Model** → **Tìm thêm model RoFormer** (có gần 90 model để chọn).

---

## Dữ liệu được lưu ở đâu?

Tất cả nằm trong `%APPDATA%\DemucsStudio` — gõ đường dẫn này vào thanh địa chỉ
File Explorer là mở được. Trong app cũng có nút **Mở thư mục dữ liệu** ở tab
**Cấu hình**.

Thư mục này chứa cấu hình và các model đã tải. Xoá nó đi là app trở về trạng
thái mới cài (phải tải lại model).

---

## Giấy phép

MIT. Demucs (MIT, Meta) và các model RoFormer giữ giấy phép riêng của tác giả.
Mã nguồn và tài liệu kỹ thuật đầy đủ: xem README trong repo dự án.
