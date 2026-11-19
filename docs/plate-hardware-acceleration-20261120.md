# ナンバー認識のハードウェア加速

2026-11-20。起点は `origin/main` の `fe9ba52`。NP1の通常実行条件に合わせ、カーネル4.14.206、CPU 6コアがオンラインの個体で検証した。

## 実装

- AArch64版TensorFlow Lite 2.16.1にOpenCLのGPU delegateを組み込む。基本読取り・かな・分類番号は、各グラフを丸ごと委譲できた場合にAdrenoを使う。検出器は実機で作ったモデル専用のOpenCLキャッシュがある場合にFP16でGPUへ委譲する。PP-OCRv6はCPUを使う。
- 初期化、割り当て、推論で失敗したモデルはCPUへ切り替える。推論の失敗時は同じ入力を再処理する。子プロセスの起動が失敗した場合はCPU指定で起動し直す。
- 推論エンジンはフレーム応答で使用中のバックエンドを返す。GPUライブラリの診断出力はJSONプロトコルから分離する。比較用の `--backend cpu` または `NCC_PLATE_BACKEND=cpu` でCPU経路を選べる。
- 同梱ONNX RuntimeはMicrosoftのNuGetパッケージから取得できるようにした。ARM64共有ライブラリのSHA-256は既存配布物と一致する。複数プレートをPP-OCRv6で照合するフレームが従来の5秒を超えたため、センサー側の応答期限を20秒にした。

## 実機の比較

純正の単体ベンチで現行の3つのTFLite読取りモデルを計測した。基本読取りはXNNPACK 25.62 msからGPU 9.50 ms、かなは5.96 msから4.53 ms、分類番号は11.05 msから5.39 msだった。いずれも36演算を1パーティションに委譲した。

現行の検出器に対し、純正Hexagon delegateは105演算中0件を委譲した。TensorFlow Lite 2.16.1のHexagon delegateでは103演算が委譲候補に入ったが、実機のHexagonランタイムはグラフ準備で失敗した。Adrenoの高精度設定はCPU約200 msに対して約216 ms。FP16設定ではCPU 206.5 msに対して129.7 msだった。

現行パッケージを使った合成陽性フレーム8枚では、CPU版とGPU読取り版で地名・分類番号・かな・一連番号の結果が全フレーム一致した。FP16検出とGPU読取りを組み合わせた11枚の比較でも、陽性3枚を含む全フレームで一致した。TFLite読取り全体は38.2 msから25.1 ms。PP-OCRv6を含む陽性フレームの処理時間中央値は5,470 msから5,363 msだった。

同梱モデル一式でのOpenCLキャッシュ作成は47.7秒。作成済みキャッシュを使うと、GPUで検出・読取りを行う子プロセスの初期化が10.9秒で完了した。キャッシュを使った検出は127.0 ms、同じ試行のCPU検出は201.6 msだった。

## 配備と復旧

実機の既存モデルと認識設定を使ったまま、所有者の認識エンジンとONNX Runtime、および認識ユニットのライブラリ配置を更新する。検出もGPUへ委譲する場合は、稼働中のセンサーを維持したまま先に実機でOpenCLキャッシュを作成し、別のプロセスで読み込んで検証する。エンジン、ユニット、既存キャッシュは `recovery/plate-update-*` に退避する。更新用スクリプトはハッシュ、稼働中のエンジンと共有ライブラリ、純正5サービスの状態と起動IDを照合する。適用時に照合に失敗するとバックアップを復元する。

```sh
bash tools/plate/build_engine.sh aarch64
python tools/device/bench-plate-engine.py --serial SERIAL --engine artifacts/plate/plate-engine-aarch64 --cache-detector --report tmp/plate-bench.json
python tools/device/deploy-plate-engine.py --serial SERIAL --prepare-detector
python tools/device/deploy-plate-engine.py --serial SERIAL --rollback RECOVERY_DIRECTORY
```

認識タブでは、GPUを含むバックエンド表示、処理済みフレームの時刻の更新、前方・車内カメラの映像を確認する。実機映像、ナンバー、端末固有情報は検証記録へ含めない。

配備後、既設モデルのハッシュと稼働中エンジンのハッシュを照合した。`gpu detector + gpu ocr` の状態でフレーム処理時刻が更新され、認識サービスの再起動回数は0だった。Communication・DriveRec・ImageRecog・Voice・QMMFのPID・起動IDは配備前後で一致した。前方と車内のJPEG、SD録画のRange読み出し、実機のナンバー画面2件が通過した。
