# DemucsStudio

Dán link YouTube → tải nhạc về máy → tách riêng **giọng hát** và **nhạc nền**.

Mọi thứ chạy ngay trên máy bạn, không cần tài khoản, không upload file đi đâu cả.

---

## 1. Cài và mở ứng dụng

Kéo **DemucsStudio** thả vào thư mục **Applications** ngay trong cửa sổ này.

Lần đầu mở, macOS sẽ báo *"DemucsStudio is damaged and can't be opened"*.
**App không hỏng** — đây là điều macOS nói với mọi ứng dụng tải từ Internet mà
chưa mua chứng chỉ ký số của Apple (99 USD/năm). Gỡ bằng một lệnh:

```bash
xattr -dr com.apple.quarantine /Applications/DemucsStudio.app
```

Mở **Terminal** (Spotlight → gõ `Terminal`), dán dòng trên, Enter. Sau đó mở app
bình thường. Chỉ phải làm một lần cho mỗi lần cài bản mới.

> Cách khác không cần Terminal: nhấp chuột phải vào app → **Open** → **Open**.
> Cách này chỉ ăn với một số phiên bản macOS; nếu vẫn báo lỗi thì dùng lệnh trên.

App chạy trên cả máy Intel và Apple Silicon (M1/M2/M3/M4) — cùng một file.

---

## 2. Cài đặt lần đầu (làm một lần duy nhất)

Mở tab **Phụ thuộc** ở đầu cửa sổ.

### Bước 1 — Công cụ tải nhạc

Bấm **Cài tự động** ở hai dòng `yt-dlp` và `ffmpeg`. Chờ vài chục giây.

Xong bước này là đã **tải nhạc từ YouTube về máy** được rồi.

### Bước 2 — Cài Python (nếu muốn tách vocal)

macOS có sẵn `python3` nhưng bản đó thiếu `venv` đầy đủ. Cài bản riêng:

```bash
brew install python@3.12
```

Chưa có Homebrew thì cài tại [brew.sh](https://brew.sh), hoặc tải bộ cài từ
[python.org/downloads/macos](https://www.python.org/downloads/macos/).

### Bước 3 — Cài bộ tách nhạc

Vẫn ở tab **Phụ thuộc**, tìm khối **Cài engine Python**, chọn **Tải bản CPU** rồi
tích engine muốn dùng và bấm **Cài engine**.

> ⚠️ Mac **không có CUDA**. Mục *Tải bản CUDA (GPU)* là dành cho máy NVIDIA, đừng
> chọn. Trên Apple Silicon, PyTorch chạy bằng CPU hoặc MPS tuỳ engine.

Lần cài này tải khá nhiều dữ liệu nên có thể mất 10–30 phút tuỳ mạng. Cứ để cửa
sổ mở, xem tiến độ ở khung **Nhật ký** phía dưới. Cài xong một lần là dùng mãi.

---

## 3. Tách nhạc

1. Vào tab **Tách nhạc**.
2. Dán link YouTube vào ô đầu tiên → bấm **Tải WAV**.
   Muốn xem trước tên bài, thời lượng thì bấm **Xem thông tin**.
3. Chờ tải xong, bài hát hiện ra kèm ảnh bìa và trình phát để nghe thử.
4. Chọn **Model** (xem bảng dưới), để **Thiết bị** ở *Tự động*.
5. Bấm **Tách vocal**.
6. Xong sẽ có hai file: giọng hát (`vocals.wav`) và nhạc nền (`no_vocals.wav`),
   nghe thử ngay trong app hoặc bấm **Mở thư mục kết quả**.

Lần đầu dùng một model, app cần tải model đó về (300 MB – 1 GB). Những lần sau
dùng lại ngay, không tải nữa.

### Nên chọn model nào?

| Bạn muốn | Chọn |
| --- | --- |
| Nhanh, hợp với mọi máy Mac | **Demucs — htdemucs_ft** |
| Chất lượng cao hơn, chấp nhận chờ | **Mel-Band RoFormer — Kim FT2** |

Model RoFormer cho giọng hát sạch hơn nhưng **chạy rất chậm khi không có GPU
NVIDIA**. Trên Mac, cứ bắt đầu bằng `htdemucs_ft`.

---

## 4. Gặp vấn đề?

**Mở app báo "is damaged and can't be opened"**
→ Chạy lệnh `xattr` ở mục 1. Đây là quarantine của macOS, không phải file hỏng.

**Không tải được video / báo lỗi lạ khi tải**
→ Tab **Phụ thuộc** → **Cập nhật yt-dlp**. YouTube thay đổi liên tục, đây là
nguyên nhân phổ biến nhất.

**Video bị chặn hoặc giới hạn độ tuổi**
→ Tab **Cấu hình** → **Cookies từ browser** → chọn trình duyệt bạn đang đăng nhập
YouTube.

**Tách nhạc rất chậm**
→ Bình thường trên Mac với model RoFormer. Đổi sang `htdemucs_ft`, hoặc giảm
**Shifts** về 1 trong tab **Cấu hình**.

**Muốn thêm model khác**
→ Tab **Model** → **Tìm thêm model RoFormer** (có gần 90 model để chọn).

---

## Dữ liệu được lưu ở đâu?

Tất cả nằm trong `~/Library/Application Support/DemucsStudio`. Trong app có nút
**Mở thư mục dữ liệu** ở tab **Cấu hình**.

Thư mục này chứa cấu hình và các model đã tải. Xoá nó đi là app trở về trạng
thái mới cài (phải tải lại model).

---

## Giấy phép

MIT. Demucs (MIT, Meta) và các model RoFormer giữ giấy phép riêng của tác giả.
Mã nguồn và tài liệu kỹ thuật đầy đủ: xem README trong repo dự án.
