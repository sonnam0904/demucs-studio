# Hướng dẫn sử dụng

Trang này giả định bạn đã làm xong [Cài đặt](installation.md). Gặp lỗi thì sang
[Xử lý sự cố](troubleshooting.md).

---

## Quy trình bốn bước

### 1 · Chọn nguồn audio

Tab **Tách nhạc**, ô đầu tiên. Hai cách:

- **Dán link YouTube** → bấm *Tải WAV*. Muốn xem trước tên bài và thời lượng thì
  bấm *Xem thông tin* trước.
- **Chọn file có sẵn trên máy** — bỏ qua hẳn bước tải.

Tải xong, bài hát hiện ra kèm ảnh bìa, tiêu đề và một player để nghe thử.

### 2 · Chọn model

Dropdown **Model** gom theo engine. Xem [Chọn model](#chon-model) bên dưới để
biết nên lấy cái nào.

### 3 · Chọn thiết bị và stem

**Thiết bị** cứ để *Tự động* — app tự kiểm tra GPU rồi quyết định. Nó không chỉ
hỏi `torch.cuda.is_available()` mà còn chạy thử một CUDA kernel thật, nên card cũ
sẽ bị loại ngay từ đầu thay vì chết giữa lúc đang tách.

**Stem** quyết định tách ra bao nhiêu file:

| Chọn | Kết quả |
| --- | --- |
| Vocals / no-vocals | `vocals.wav` + `no_vocals.wav` — thường dùng nhất |
| Tất cả stem | vocals, drums, bass, other *(và guitar, piano nếu dùng `htdemucs_6s`)* |

### 4 · Tách

Bấm **Tách vocal**. Thanh trạng thái dưới cùng chạy tiến độ thật — với
`htdemucs_ft` là tiến độ theo từng sub-model trong bag, không phải thanh giả.

Muốn xem app đang làm gì thì mở khung **Nhật ký** — nguyên văn output của yt-dlp,
demucs và audio-separator.

Bấm **Huỷ** là dừng thật, kể cả các process con mà demucs sinh ra.

Xong, mỗi stem có player riêng để nghe thử ngay, và nút **Mở thư mục kết quả**.

---

## Chọn model { #chon-model }

| Engine | Model | Ghi chú |
| --- | --- | --- |
| **Demucs v4** | `htdemucs_ft` *(đề xuất)* | Bag of 4 model, chất lượng cao nhất của Demucs — 4 × 84 MB ≈ 328 MB |
| | `htdemucs` | Một lượt chạy, nhanh hơn ~4× |
| | `htdemucs_6s` | Thêm stem guitar và piano |
| **BS-RoFormer** | Vocals Resurrection, Vocals Revive V3e, SW, Karaoke… | Chất lượng vocal SOTA, nên có GPU |
| **Mel-Band RoFormer** | Kim FT2 *(đề xuất)*, Big Beta 6X, Vocals, Karaoke… | Nhanh hơn BS-RoFormer, chất lượng vẫn rất tốt |

Nhanh gọn:

- **Chỉ có CPU** → `htdemucs_ft`.
- **Có GPU NVIDIA, muốn chất lượng tối đa** → BS-RoFormer Vocals Resurrection.
- **Có GPU, muốn cân bằng** → Mel-Band RoFormer Kim FT2.

!!! warning "RoFormer chạy bằng CPU thì rất chậm"

    Đo trên clip 19 giây, CPU Intel i5-11400: `htdemucs_ft` mất **35 giây**,
    Mel-Band Kim FT2 mất **172 giây** — gấp gần 5 lần. Có GPU thì RoFormer nhanh
    hơn nhiều lần. Không có card NVIDIA thì cứ dùng `htdemucs_ft`.

### Thêm model khác

10 model RoFormer đã được chọn sẵn. Muốn nhiều hơn: tab **Model** → *Tìm thêm
model RoFormer*. Nút này đọc danh sách trực tiếp từ `audio-separator --list_models`
— hiện khoảng 89 model.

---

## Model được tải về đâu

Lần đầu dùng một model, app tải nó về rồi dùng lại mãi:

| Engine | Vị trí | Dung lượng |
| --- | --- | --- |
| Demucs | `<thư mục dữ liệu>/models/demucs/` | 84 MB mỗi file, `htdemucs_ft` cần 4 file |
| RoFormer | `<thư mục dữ liệu>/models/roformer/` | 400–900 MB tuỳ model |

Đường dẫn `<thư mục dữ liệu>` ở
[phần này](installation.md#thu-muc-du-lieu).

Vài điểm đáng biết:

- Tải có **progress bar thật** và verify checksum, thay vì để engine âm thầm tải
  giữa lúc đang tách.
- **Nếu máy từng chạy demucs**, app copy thẳng từ `~/.cache/torch/hub/checkpoints`
  — khỏi tải lại.
- Sau lần tải đầu, việc tách nhạc **chạy hoàn toàn offline**.

Chi tiết cơ chế ở [Ghi chú kỹ thuật](engineering.md).

---

## Tinh chỉnh

Tab **Cấu hình**. Mặc định dùng được ngay; chỉ động vào khi cần.

### Demucs

| Tham số | Tác dụng | Khi nào đổi |
| --- | --- | --- |
| **Shifts** | Chạy lại nhiều lần với offset khác nhau rồi lấy trung bình | Tăng lên 2–5 để có chất lượng cao nhất. Thời gian chạy tăng đúng bằng hệ số đó |
| **Segment** | Độ dài mỗi đoạn xử lý | Giảm về 5–7 khi hết VRAM |

### RoFormer

| Tham số | Tác dụng | Khi nào đổi |
| --- | --- | --- |
| **Segment size** | Độ dài cửa sổ, mặc định 256 | Giảm về 128 khi hết VRAM |
| **Batch size** | Số đoạn xử lý cùng lúc | Giữ = 1 trên GPU ít VRAM |

### Cookies từ browser

Cần cho video bị chặn hoặc giới hạn độ tuổi. Chọn trình duyệt bạn đang đăng nhập
YouTube, app sẽ mượn cookie từ đó.

---

## Mẹo

- **Chạy lại nhanh** — model đã tải nằm trong `models/`, lần sau không tải lại.
- **Chất lượng tối đa** — `htdemucs_ft` với *Shifts* = 2–5, hoặc BS-RoFormer
  Vocals Resurrection nếu có GPU.
- **Xử lý file có sẵn** — không nhất thiết phải qua YouTube, chọn thẳng file
  audio trên máy.
- **Đổi chỗ lưu dữ liệu** — đặt biến môi trường `DEMUCS_STUDIO_HOME`.
