# Majdata Provider

Majdata Provider adalah server HTTP berbasis Go untuk menyediakan chart online bagi MajdataPlay. Server memindai folder `charts/` secara rekursif untuk mencari `maidata.txt`, lalu menyajikan metadata dan aset chart. Server ini juga menyediakan pendaftaran khusus undangan, login yang kompatibel dengan game, serta penyimpanan skor pemain menggunakan SQLite.

## Menjalankan

Memerlukan Go 1.25 atau yang lebih baru.

```powershell
$env:MAJDATA_INVITE_CODE = "ganti-dengan-kode-undangan-acak-yang-panjang"
go run .
```

Server mendengarkan pada port `8080` dan otomatis membuat `data/majdata.sqlite`. Lokasi database dapat diubah dengan `MAJDATA_DB_PATH`.

## Menambahkan chart

Letakkan setiap chart di folder tersendiri di bawah `charts/`. Kedalaman folder bebas; chart akan ditemukan jika foldernya berisi `maidata.txt`.

```text
charts/
  dx_circle_plus/
    chart_id/
      maidata.txt
      track.mp3
      bg.jpg
```

Setelah mengubah chart, mulai ulang server agar manifest dan katalog dalam memori dibuat ulang.

## Menghubungkan MajdataPlay

Atur `Online.ApiEndPoints` di `settings.json` game. URL harus diakhiri dengan `/api/`:

```json
{
  "Online": {
    "Enable": true,
    "ApiEndPoints": [{
      "Name": "MyChartSource",
      "Url": "https://domain-anda.example/api/"
    }]
  }
}
```

API chart mencakup `/api/info`, `/api/maichart/list`, rute informasi dan aset chart, serta `/api/maichart/changes`.

## Akun dan skor

Pendaftaran hanya dapat dilakukan dengan kode undangan. Atur `MAJDATA_INVITE_CODE` sebelum menjalankan server dan berikan kode tersebut hanya kepada orang yang diizinkan mendaftar. Buat akun dengan `POST /api/account/register` dan JSON berisi `username`, `password`, dan `inviteCode`. Username harus terdiri dari 3–32 huruf, angka, garis bawah, titik, atau tanda hubung; password harus terdiri dari 10–128 karakter. Halaman pendaftaran HTML tidak lagi tersedia.

Protokol login MajdataPlay mengirimkan digest MD5 huruf kecil dari password yang dimasukkan. Formulir pendaftaran menyimpan hash Argon2id dari digest protokol tersebut, bukan digest MD5-nya secara langsung. Karena nilai MD5 dalam protokol game setara dengan kredensial password, endpoint akun dan skor hanya boleh diakses melalui HTTPS. Untuk penggunaan publik, letakkan server di belakang reverse proxy TLS seperti Caddy.

Client dapat login melalui `/api/account/login`, membaca atau memperbarui profil melalui `/api/account/profile`, mengambil skor terbaik melalui `/api/account/scores`, serta logout melalui `/api/account/logout`. `GET` dan `POST /api/account/gamesetting` membaca atau menyimpan pengaturan game JSON mentah milik pengguna saat ini. Skor yang dikirim ke `/api/maichart/{chartId}/score` disimpan di SQLite. Daftar skor menyimpan hasil terbaik untuk setiap tingkat kesulitan chart, dengan urutan berdasarkan akurasi classic lalu skor DX. Database default berada di `data/majdata.sqlite`; Git mengabaikan folder `data/`.

Leaderboard publik per chart tersedia melalui `GET /api/maiscore/leaderboard/chart/by-id/{chartId}`. Delapan array `scores` diurutkan berdasarkan indeks tingkat kesulitan; setiap pemain hanya ditampilkan sekali per tingkat dengan hasil akurasi classic terbaik, sementara skor DX dan waktu menjadi pemecah seri. Username pemain ditampilkan secara publik. Leaderboard global/agregat, verifikasi email, pemulihan password, dan administrasi akun belum tersedia. Lindungi kode undangan dan cadangan database.

## Playlist, rekomendasi, dan rating

Setiap folder di bawah `collections/` yang berisi `playlist.json` mendefinisikan satu koleksi publik hanya-baca. Nama yang ditampilkan berasal dari `name`, sedangkan ID koleksi yang stabil dibuat dari nama folder; `levelIds` dicocokkan dengan nama folder chart (songId). ID yang chart-nya tidak tersedia di `charts/` akan dilewati. Setiap chart tetap disimpan satu kali secara fisik dan dapat muncul di beberapa koleksi. `collections/manifest.json` dibuat ulang saat server mulai.

Koleksi diurutkan berdasarkan nama tanpa daftar seri yang di-hardcode.

Folder staging `charts/uncategorized/` yang kosong akan dihapus saat startup. Jika berisi chart, chart tetap di lokasi tersebut dan dimasukkan ke semua playlist yang cocok, sehingga ID chart berbasis path tetap sama.

`GET /api/collection/list` menampilkan koleksi publik yang ditemukan dan playlist yang dapat diakses pengguna saat ini. Detail item tersedia melalui `/api/collection/{id}/songlist` dan `/api/collection/{id}/hashlist`. Pengguna yang login dapat mengelola playlist sendiri melalui `/api/collection/create`, `PUT /api/collection/{id}/modify`, dan `/api/collection/{id}/destroy`, serta mengelola favorit melalui `POST` atau `DELETE /api/account/favorite/collection/diff`. Koleksi yang dibuat dari file bersifat hanya-baca; playlist pengguna disimpan di SQLite dan mendukung visibilitas publik atau privat.

Feed rekomendasi dan rating B30 belum diimplementasikan. MajdataPlay meminta halaman rekomendasi melalui `GET /recommend/feed` dan mengirim laporan impresi ke `POST /recommend/impressions`; permintaan klien saja tidak mengungkap algoritma backend rekomendasi.

Antarmuka rating MajdataNet yang ditinjau menjelaskan radar Best50. Hal itu bukan bukti adanya formula B30, jadi proyek ini tidak mengklaim mengimplementasikan salah satu model rating tersebut.

## Lisensi dan konten yang di-host

Lihat [LICENSE.txt](LICENSE.txt). Anda bertanggung jawab memastikan chart, musik, gambar, dan konten lain yang di-host boleh didistribusikan secara legal.
