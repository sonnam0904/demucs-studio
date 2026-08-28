# DemucsStudio

**Dán link YouTube → tải nhạc về máy → tách riêng giọng hát và nhạc nền.**

Ứng dụng desktop cho Linux và Windows. Mọi thứ xử lý ngay trên máy bạn: không
cần tài khoản, không upload file đi đâu, và sau lần tải model đầu tiên thì việc
tách nhạc chạy hoàn toàn offline.

<div class="grid cards" markdown>

- :material-download: **[Cài đặt](installation.md)**

    Tải bản dựng cho Linux hoặc Windows, rồi để app tự cài phụ thuộc.

- :material-play-circle: **[Hướng dẫn sử dụng](usage.md)**

    Quy trình bốn bước, chọn model, chỉnh tham số cho máy của bạn.

- :material-lifebuoy: **[Xử lý sự cố](troubleshooting.md)**

    Tải lỗi 403, GPU hết VRAM, video bị chặn, tách quá chậm.

- :material-hammer-wrench: **[Build từ source](build.md)**

    Dành cho người phát triển: các lệnh `make` và yêu cầu từng nền tảng.

</div>

---

## Nó làm gì

```
1 · Nguồn audio    [ https://youtube.com/watch?v=… ]   [Xem thông tin] [Tải WAV]
                   …hoặc chọn một file audio có sẵn trên máy

2 · File nguồn     thumbnail + tiêu đề + player nghe thử

3 · Tách vocal     Model ▾   Thiết bị ▾   Stem ▾            [Tách vocal]

4 · Kết quả        vocals.wav · no_vocals.wav
                   player riêng cho từng stem + mở thư mục
```

Thanh trạng thái dưới cùng chạy tiến độ thật — tải theo byte, tách theo từng
sub-model — kèm ô nhật ký xem được nguyên văn output của yt-dlp, demucs và
audio-separator. Bấm **Huỷ** là dừng thật, kể cả các process con.

## Điểm chính

- **Hai engine tách nhạc** — Demucs v4 và họ RoFormer, chọn trong cùng một
  dropdown.
- **Model lưu cục bộ, tải một lần.** App tự quản lý weight trong thư mục của
  mình với progress bar thật, thay vì để engine âm thầm tải giữa lúc đang tách.
- **Tự lo phụ thuộc.** Thiếu `yt-dlp` hay `ffmpeg` thì bấm một nút là app tải bản
  chính thức về và đối chiếu checksum công bố. Demucs và audio-separator được cài
  vào môi trường Python riêng của app, không đụng tới Python hệ thống.
- **Không tin GPU một cách mù quáng.** App chạy thử một CUDA kernel thật trước
  khi quyết định dùng GPU, nên card cũ không chết giữa lúc đang tách.
- **Nghe thử ngay trong app** — từng stem có player riêng, seek được.

## Chọn model nào?

| Bạn muốn | Chọn | Ghi chú |
| --- | --- | --- |
| Nhanh, máy chỉ có CPU | `htdemucs_ft` | Bag of 4 model, ~328 MB |
| Chất lượng cao nhất, có GPU NVIDIA | BS-RoFormer Vocals Resurrection | Nặng, cần GPU mới thực dụng |
| Cân bằng, có GPU | Mel-Band RoFormer Kim FT2 | Nhanh hơn BS-RoFormer |

RoFormer nặng hơn Demucs đáng kể. Đo trên clip 19 giây, CPU Intel i5-11400 —
`htdemucs_ft` mất **35 giây**, Mel-Band Kim FT2 mất **172 giây**. Có GPU thì
RoFormer nhanh hơn nhiều lần; chỉ có CPU thì `htdemucs_ft` thực dụng hơn.

Danh sách đầy đủ ở [Hướng dẫn sử dụng](usage.md#chon-model).

## License

MIT. Demucs (MIT, Meta) và các model RoFormer giữ license riêng của tác giả —
kiểm tra trước khi dùng cho mục đích thương mại.
