# ナンバー認識のNEON前処理・全モデルキャッシュ・GPU∥CPUヘテロジニアス並列化

2026-11-20。起点は `27efc7e`。NP1（QCS610: Kryo 460 Silver × 6 オンライン + Adreno 612 GPU）のハードウェア実力をさらに引き出すため、検出前処理・OpenCL キャッシュ・呼び出し構造・PP-OCRv6 の GPU∥CPU ヘテロジニアス並列化を本体へ組み込み、実機のクリーン条件（`np1-control-plate.service` を一時停止して GPU 競合を排除）で検証した。

## 実装内容

1. **Detector 前処理の ARM NEON 1/2 整数縮小**（[`tools/plate/engine/plate_engine.cc`](../tools/plate/engine/plate_engine.cc)）
   - 検出器の既定 ROI `[320, 240, 1280, 768]` → 入力 `640×384` はちょうど 1/2 整数縮小であり、NV12 の UV プレーンと 1:1 に対応する。
   - `float32` / `int8` 入力それぞれに ARM NEON 8 画素並列パス（`fill_det_neon_half_f32` / `fill_det_neon_half_int8`）を追加し、単体ベンチでは `21.99 ms → 1.04 ms`（ビット完全一致）となった。ROI 左端が奇数の場合（`(rx & 1) != 0`）やその他の ROI・解像度では、NV12 の UV 座標ずれを防ぐため従来の汎用経路へフォールバックする。
2. **TFLite 全 4 モデルの OpenCL シリアライズキャッシュ化**
   - 従来は検出器のみ OpenCL キャッシュ（`ENABLE_SERIALIZATION`）を使っていたが、`ocr.tflite`・`ocr-kana.tflite`・`ocr-classification.tflite`（FP32 `MAX_PRECISION` 維持）にもキャッシュを拡大した。
   - 通常起動時は `.ready` マーカーが確認できたキャッシュだけを読み取り専用で参照し、`/overlay` が Read-Only の systemd サンドボックス下でも書き込みエラーを起こさない。`--prepare-gpu-cache` 実行時に全モデルのキャッシュとマーカーを一括生成する。
   - なお、`ocr*.tflite` の FP16 化は 17 枚比較で 1 件の差分が出たため不採用とした。
3. **記録しきい値連動の `number_candidate` 先行による早期スキップ**
   - `plate-engine` に `--min-confidence <値>`（既定 `0.3`、ワーカー側 `platesensor.ReadMinConfidence` が `settings.json` の `minConfidence` を読み取り反映、設定変更時はエンジンを再起動）を追加した。
   - `base_serial.confidence < min_confidence_` の候補について、先に `number_candidate` を 1 回だけ評価し、依然設定下限未満（記録対象外が確定）であれば地名・かな・夜間フォールバック（最大 12 回）の PP-OCRv6 実行を省略する。記録しきい値を `0.6` 未満（例: `0.3`）に下げた場合も、`0.3〜0.59` 帯の記録対象候補で地名・かな・分類番号が欠落しない。
4. **MNN OpenCL キャッシュ済バッチ ∥ ONNX INT8 CPU ヘテロジニアス並列と整合性検証**
   - ビルドスクリプト [`tools/plate/build_engine.sh`](../tools/plate/build_engine.sh) に MNN 3.2.5（Apache-2.0、`MNN_OPENCL=ON`, `MNN_ARM82=ON`, `MNN_LOW_MEMORY=ON`）の静的リンクを追加し、重み 8 bit 圧縮モデル [`models/plate/ppocrv6_rec_np1_w8.mnn`](../models/plate/ppocrv6_rec_np1_w8.mnn)（19.5 MB）を同梱した。
   - `--prepare-gpu-cache` により固定幅 64・既定 Batch=3 の OpenCL キャッシュ（約 613 KB）と、サイズおよびファイル指紋を含む `.ready` マーカーを事前生成する。従来試作の課題だったコールド起動 30 秒超はキャッシュ読み込みにより 1.1〜1.6 秒（エンジン全体 4.3〜4.8 秒）へ短縮した。
   - 通常起動時は MNN へキャッシュを渡す前に `.ready` 記載のサイズ・指紋と実ファイルを照合するため、キャッシュが破損していた場合でも Read-Only `/overlay` 上での再コンパイル・書き込みエラーを起こさず即座に ONNX INT8 CPU へフォールバックする。
   - `MnnGpuBatch::run_batch()` は `gpuBatch`（1〜4）ごとにチャンク分割して推論し、入力件数分の結果を必ず返すため、`gpuBatch=1` や `2` に設定しても地名 3 クロップの範囲外読み出しが起きない。
   - 推論時は `std::async` により Adreno 612（MNN OpenCL W8：`region` 3 クロップおよび夜間上段文脈）と Kryo 460 CPU（ONNX Runtime INT8 可変幅上限 128：`number`・`kana`・夜間下段文脈）を同時並列実行する。キャッシュ未作成・破損時や推論失敗時は自動的に ONNX INT8 CPU のみへフォールバックする。

## 実機のクリーン比較（公式ビルド `plate-engine-aarch64`）

計測中のみ `np1-control-plate.service` を停止して GPU 負荷 `0/0` を確認し、`CPUQuota=150%`・`MemoryLimit=384M`・`Nice=10` の本番制限下で旧ビルド（`baseline`）と新ビルド（`candidate`、キャッシュディレクトリを `chmod 0555/0444` で Read-Only 化）を比較した。全計測後にサービスを復旧し、純正 5 サービス（Communication / DriveRec / ImageRecog / Voice / qmmf-server）の状態・PID・前方カメラ更新・SD 録画継続を照合した。

### 1. 同梱 PP-OCRv6 込み構成（`quality-final.nv12` 17 枚 / `public.nv12` 6 枚）

| データセット・構成 | 起動時間 (`init`) | 陽性中央値 | 最悪値 (`max`) | Detector | TFLite OCR | Text OCR | 全項目一致 | 記録一致 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | :---: | :---: |
| `quality17` 旧ビルド (`baseline`) | 10,954 ms | 1,452.4 ms | 3,110.4 ms | 128.2 ms | 33.1 ms | 1,310.1 ms | 基準 (17/17) | 基準 (17/17) |
| **`quality17` 新ビルド (RO キャッシュ・並列)** | **4,288 ms (-61%)** | **828.3 ms (-43%)** | **1,460.3 ms (-53%)** | **98.8 ms** | 36.5 ms | **687.4 ms (-48%)** | **17/17** | **17/17** |
| `public6` 旧ビルド (`baseline`) | 10,895 ms | 1,006.6 ms | 1,086.3 ms | 127.5 ms | 17.6 ms | 858.6 ms | 基準 (6/6, 4完) | 基準 (6/6) |
| **`public6` 新ビルド (RO キャッシュ・並列)** | **4,449 ms (-59%)** | **535.2 ms (-47%)** | **625.0 ms (-42%)** | **99.3 ms** | 19.8 ms | **418.1 ms (-51%)** | **6/6 (4完一致)** | **6/6** |

- `--prepare-gpu-cache` による全 5 モデル一括キャッシュ生成: `73.7 秒`（`detector` + `ocr` 3 種 + `ppocr` W8 B=3 のすべてに `.ready` マーカー生成を確認）。
- 空の Read-Only キャッシュディレクトリ起動テスト: `11.08 秒` で安全に CPU 検出 + GPU TFLite OCR + ONNX INT8 CPU へ自動フォールバックし、書き込みエラーなし。
- 稼働バックエンド表記: `xnnpack cpu x2 + gpu detector + gpu ocr + gpu kana + gpu class + gpu ppocr + onnx int8 cpu`。

### 2. 既設 TFLite のみ構成（実機常時稼働モデル）

| 構成 | 起動時間 (`init`) | `detectorMs`（8 回平均） | `readerMs`（8 回平均） |
| --- | ---: | ---: | ---: |
| 旧ビルド | 3,799.1 ms | 125.66 ms | 14.01 ms |
| **新ビルド（NEON 前処理 + OCR キャッシュ）** | **1,077.0 ms (-72%)** | **98.11 ms (-22%)** | **13.09 ms** |

### 3. 本番同一 Read-Only サンドボックス・境界条件検証（最終版エンジン）

`np1-control-plate.service` と同一の systemd サンドボックス（`ProtectSystem=strict`, `ReadOnlyPaths=/overlay`, `CapabilityBoundingSet=CAP_DAC_READ_SEARCH`, `NoNewPrivileges=yes`, `CPUQuota=150%`, `MemoryLimit=384M`, `Nice=10`）を `systemd-run` で再現し、`/overlay` への書き込みが `root` でも `Read-only file system` で拒否される条件下で検証した。

- **Read-Only キャッシュ検証（全 4 パターンで書き込みエラー 0 件）**:
  - 正常キャッシュ: `init = 4,792.8 ms`、`gpu ppocr` 有効、書き込みエラー 0 件
  - サイズ破損 `.mnncache`（`.ready` 残存）: `init = 3,638.1 ms`、整合性チェックで即時 CPU フォールバック、書き込みエラー 0 件
  - 同サイズ 4 バイト改ざん `.mnncache`（`.ready` 残存）: `init = 3,719.4 ms`、整合性チェックで即時 CPU フォールバック、書き込みエラー 0 件
  - 空キャッシュ: `init = 11,052.1 ms`、未準備検知で即時 CPU フォールバック、書き込みエラー 0 件
- **`gpuBatch` 分割動作（`1` / `2` / `3`）**: `quality17`（17/17）・`public6`（6/6）とも全バッチ設定でクラッシュゼロ・結果完全一致。
- **`--min-confidence` 連動**: `--min-confidence 0.3` / 既定値 (`0.3`) では低信頼度帯 (`0.30〜0.59`) の補完を省略せず（`max = 1,717〜1,771 ms`）、`--min-confidence 0.6` では設定下限未満のみ早期スキップ（`max = 1,377.8 ms`）。ワーカー側の設定読込・変更時再起動は `backend/internal/platesensor/sensor_test.go` で単体試験済み。
- **奇数 ROI (`rx = 321`)**: `detectorMs = 129.2 ms` で汎用前処理へフォールバックし、UV 座標ずれが発生しないことを確認。

## 配備と復旧

既設機へ PP-OCRv6 GPU 経路を含めて一括反映する場合は、`--models models/plate` と `--worker`（`--min-confidence` 伝播対応の ARM64 `ncc`）を付けて配備する。`plate-engine-update.sh` は `plate-model.json` に `"gpuFile"` がある場合にキャッシュ検証で `gpu detector` と `gpu ppocr` の両方を必須チェックし、失敗時は自動ロールバックする。

モデル一式のコピーは認識サービスの停止前に完了させる。コピーが失敗した場合は既設モデルでの認識を継続する。

```sh
bash tools/plate/build_engine.sh all
# 既設機へエンジン・ワーカー・PP-OCRv6 含むモデル一式・全 5 モデルキャッシュを配備
python tools/device/deploy-plate-engine.py --serial SERIAL \
  --worker artifacts/ncc/bin/ncc \
  --models models/plate \
  --prepare-detector
# 既設モデル構成を維持したままエンジンのみ更新する場合
python tools/device/deploy-plate-engine.py --serial SERIAL --prepare-detector
# 復旧（配備時に表示された RECOVERY_DIRECTORY を指定）
python tools/device/deploy-plate-engine.py --serial SERIAL --rollback RECOVERY_DIRECTORY
```

実機へ一式配備後、稼働中バイナリの SHA-256（エンジン `37706d70f848e8a005abe91903492b7739dc18e5a04d58ef721741f28d045cc1`、ワーカー `d708fa28d62a553b1b6c8e499c7dc25a49426f4cc145fd4d99431741650c901d`）、全 5 モデル（`detector` / `ocr` / `ocr-kana` / `ocr-classification` / `ppocr` W8 B=3）の OpenCL キャッシュおよび read-back（`initMs=5,393 ms`, `gpu ppocr` 確認）、`np1-control-plate.service` の稼働状態（`ActiveState=active`, `NRestarts=0`, メモリ使用量約 217 MiB / 上限 384 MiB、journal 書き込みエラー 0 件）、認識センサー状態（`state=running`, `backend=xnnpack cpu x2 + gpu detector + gpu ocr + gpu kana + gpu class + gpu ppocr + onnx int8 cpu`, 3.99 fps、`lastFrameAt` 継続更新）、および純正 5 サービスの PID・起動 ID・録画継続を確認した。
