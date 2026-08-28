# Cài đặt

Ba bước: cài ứng dụng → cài công cụ tải nhạc → cài engine tách nhạc.

Chỉ muốn **tải audio** từ YouTube thì làm đến bước 2 là đủ. Muốn **tách vocal**
thì cần thêm bước 3 và một bản Python cài sẵn trên máy.

---

## 1. Cài ứng dụng

Tải bản dựng mới nhất ở trang
[Releases](https://github.com/sonnam0904/demucs-studio/releases).

=== "Linux"

    Bản cài đặt — tích hợp vào menu ứng dụng:

    ```bash
    sudo apt install ./demucs-studio_x.y.z_amd64.deb    # Debian, Ubuntu, Pop!_OS
    sudo dnf install ./demucs-studio-x.y.z-1.x86_64.rpm # Fedora, RHEL
    ```

    Bản portable — không cần quyền root, chạy từ đâu cũng được:

    ```bash
    tar -xzf demucs-studio-x.y.z-linux-amd64.tar.gz
    ./demucs-studio/run.sh
    ```

    Gói `.deb`/`.rpm` khai báo `libwebkit2gtk-4.1-0` và `libgtk-3-0` là dependency
    nên trình quản lý gói tự kéo về. Bản portable thì bạn tự đảm bảo hai thư viện
    đó có trên máy.

=== "Windows"

    1. Giải nén `demucs-studio-x.y.z-windows-amd64.zip` ra thư mục bất kỳ.
    2. Chạy `demucs-studio.exe`.

    Copy nguyên thư mục đi đâu cũng được, kể cả USB — app không ghi gì vào
    `Program Files` hay registry.

    !!! warning "Windows chặn lần đầu mở"

        Hiện cảnh báo *"Windows protected your PC"* → bấm **More info** →
        **Run anyway**. Ứng dụng chưa mua chứng chỉ ký số nên SmartScreen cảnh báo
        theo mặc định.

    !!! info "Windows 10 cần WebView2"

        Nếu app báo thiếu WebView2, cài
        [WebView2 Runtime](https://developer.microsoft.com/microsoft-edge/webview2/)
        rồi mở lại. Windows 11 đã có sẵn.

=== "macOS"

    1. Mở `demucs-studio-x.y.z-macos-universal.dmg`.
    2. Kéo **DemucsStudio** thả vào **Applications**.

    Một file chạy được trên cả Intel lẫn Apple Silicon — bản dựng là universal
    binary, đã lipo sẵn hai kiến trúc.

    !!! warning "macOS báo *"is damaged and can't be opened"*"

        App không hỏng. Ứng dụng chưa mua chứng chỉ Developer ID của Apple nên
        Gatekeeper chặn mọi bản tải từ Internet. Gỡ cờ quarantine một lần:

        ```bash
        xattr -dr com.apple.quarantine /Applications/DemucsStudio.app
        ```

        Phải làm lại sau mỗi lần cài bản mới.

---

## 2. Cài công cụ tải nhạc

Mở tab **Phụ thuộc** → bấm *Cài tự động* ở hai dòng `yt-dlp` và `ffmpeg`.
Mất vài chục giây.

| Công cụ | Bắt buộc | App tự cài được? |
| --- | --- | --- |
| `yt-dlp` | ✅ để tải | ✅ tải bản standalone từ GitHub release |
| `ffmpeg` | ✅ để chuyển sang WAV | ✅ tải bản static, kèm `ffprobe` |
| JS runtime (`deno`/`node`/`bun`) | nên có | ❌ cài sẵn ở hệ thống |
| Python 3.10+ | chỉ khi muốn tách nhạc | ❌ cài sẵn ở hệ thống |
| `demucs` | cho engine Demucs | ✅ pip vào venv riêng của app |
| `audio-separator` | cho engine RoFormer | ✅ pip vào venv riêng của app |

!!! tip "Nên cài thêm một JS runtime"

    YouTube bắt giải một thử thách JavaScript để lấy URL media. Thiếu runtime thì
    yt-dlp mất một số format và coi đường extraction đó là deprecated. App tự dò
    `deno`/`node`/`bun` rồi truyền sang yt-dlp giúp bạn — nhưng phải có ít nhất
    một cái. `deno` nhẹ nhất.

Xong bước này là **tải nhạc từ YouTube về máy** được rồi.

---

## 3. Cài engine tách nhạc

### 3.1 Cài Python

Cần **Python 3.10 trở lên**, cài sẵn ở hệ thống. Đây là thứ duy nhất app không
tự cài được.

=== "Linux"

    ```bash
    sudo apt install python3 python3-venv   # Debian, Ubuntu, Pop!_OS
    sudo dnf install python3                # Fedora, RHEL
    ```

    Gói `python3-venv` là bắt buộc trên Debian/Ubuntu — thiếu nó thì app không tạo
    được môi trường riêng và bước 3.2 sẽ lỗi.

=== "Windows"

    Tải **Python 3.11 hoặc 3.12** tại
    [python.org/downloads/windows](https://www.python.org/downloads/windows/).

    !!! danger "Hai chỗ dễ sai"

        - Trong màn hình cài đặt, **nhớ tích ô "Add python.exe to PATH"** ở dưới cùng.
        - **Đừng cài Python từ Microsoft Store.** App phát hiện và từ chối stub
          `WindowsApps\python.exe`, vì chạy nó chỉ mở Store chứ không phải
          interpreter. App ưu tiên launcher `py -3` nên bản python.org luôn được
          tìm thấy dù PATH có sạch hay không.

=== "macOS"

    ```bash
    brew install python@3.12
    ```

    Chưa có Homebrew thì cài tại [brew.sh](https://brew.sh), hoặc tải bộ cài từ
    [python.org/downloads/macos](https://www.python.org/downloads/macos/).

    `python3` có sẵn trong Command Line Tools cũng chạy được, nhưng bản Homebrew
    hoặc python.org tránh được các hạn chế của interpreter do Apple quản lý.

### 3.2 Cài engine

Vẫn ở tab **Phụ thuộc**, tìm khối **Cài engine Python**.

Chọn bản PyTorch phù hợp với máy:

| Chọn | Khi nào |
| --- | --- |
| **Tải bản CUDA (GPU)** + CUDA `cu124` | Có GPU NVIDIA — gần như bắt buộc nếu muốn dùng RoFormer |
| **Tải bản CPU** | Không có GPU NVIDIA |
| **Dùng lại bản đã có** | Máy đã cài sẵn PyTorch đúng loại |

!!! warning "Trên macOS đừng chọn bản CUDA"

    Mac không có CUDA — kể cả máy Intel đời cũ từng gắn card rời. Chọn **Tải bản
    CPU**; PyTorch sẽ chạy bằng CPU hoặc MPS tuỳ engine.

Rồi tích engine muốn dùng và bấm *Cài engine*:

- **demucs (Demucs v4)** — nhẹ hơn, chạy được bằng CPU.
- **audio-separator (RoFormer)** — chất lượng cao hơn, nên có GPU.

App tạo môi trường Python riêng tại `<thư mục dữ liệu>/pyenv`, **không đụng tới
Python hệ thống**. Theo dõi tiến độ ở khung *Nhật ký* dưới cùng.

!!! note "Bản CUDA nặng ~2.5 GB"

    Lần cài đầu có thể mất 10–30 phút tuỳ mạng. Cứ để cửa sổ mở. Cài xong một lần
    là dùng mãi.

!!! info "Vì sao app không để pip tự chọn torch"

    Trên Windows, `pip install torch` từ PyPI mặc định ra **bản CPU**. Nên app luôn
    cài torch từ index riêng của PyTorch (`download.pytorch.org/whl/cu124`) — nếu
    không, bạn sẽ có GPU nhưng vẫn chạy bằng CPU mà không biết.

---

## Kiểm tra lại

Quay lại tab **Phụ thuộc**, mọi dòng bạn cần nên hiện trạng thái đã tìm thấy.
Badge thiết bị ở góc trên cho biết app sẽ dùng GPU hay CPU — nếu có GPU NVIDIA mà
badge vẫn báo CPU, xem [Xử lý sự cố](troubleshooting.md#gpu).

Sẵn sàng rồi thì sang [Hướng dẫn sử dụng](usage.md).

---

## Ứng dụng tìm công cụ ở đâu { #ung-dung-tim-cong-cu-o-dau }

Với mỗi công cụ, app lấy cái đầu tiên tìm thấy theo thứ tự:

1. đường dẫn bạn tự khai trong tab **Cấu hình**
2. venv do app quản lý — `<thư mục dữ liệu>/pyenv`
3. binary app tự tải — `<thư mục dữ liệu>/bin`
4. binary đóng gói kèm — `<thư mục chứa exe>/bin` *(bản portable)*
5. `PATH` của hệ thống

Thứ tự này là lý do bản yt-dlp do app tải luôn thắng bản cũ nằm trong `PATH`.

## Thư mục dữ liệu { #thu-muc-du-lieu }

| | |
| --- | --- |
| Linux | `~/.local/share/demucs-studio` |
| Windows | `%APPDATA%\DemucsStudio` |
| macOS | `~/Library/Application Support/DemucsStudio` |

Bên trong: `settings.json`, `bin/`, `models/demucs/`, `models/roformer/`,
`pyenv/`. Đặt biến môi trường `DEMUCS_STUDIO_HOME` để đổi chỗ.

Xoá thư mục này là app trở về trạng thái mới cài — phải tải lại model và cài lại
engine. Trong app có nút **Mở thư mục dữ liệu** ở tab **Cấu hình**.

## Gỡ cài đặt

=== "Linux"

    ```bash
    sudo apt remove demucs-studio        # hoặc: sudo dnf remove demucs-studio
    rm -rf ~/.local/share/demucs-studio  # model và cấu hình
    ```

=== "Windows"

    Xoá thư mục đã giải nén, rồi xoá `%APPDATA%\DemucsStudio`.

=== "macOS"

    ```bash
    rm -rf /Applications/DemucsStudio.app
    rm -rf ~/Library/Application\ Support/DemucsStudio   # model và cấu hình
    ```
