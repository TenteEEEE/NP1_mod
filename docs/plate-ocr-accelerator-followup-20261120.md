# PP-OCRv6のGPU・DSP検証と認識速度の改善

2026-11-20。起点は `15f1c20`。実機でQCS610、Adreno 612、既設SNPEを確認した。QualcommはQCS610にHexagon 685とAdreno 612を記載している。[QCS610の仕様](https://www.qualcomm.com/internet-of-things/products/q6-series/qcs610)

## 実行先の比較

ONNXからMNNに変換したPP-OCRv6を、実機のOpenCLで動かした。MNN 3.2.5のバッファ方式、FP16、CPU 150%・メモリ384 MiB制限で、合成した入力8個を測った。グラフはOpenCLで実行され、入力幅31～224で1推論373～1,029 msだった。同じ入力のINT8 CPU版は69～369 msだった。8入力の各幅でCPU版のほうが短時間で処理した。MNNのFP16出力は元のONNXと各時刻の最尤文字が一致したが、最大確率差は0.218だった。[MNNのOpenCL対応と変換機能](https://github.com/alibaba/MNN)

画像方式に切り替えると、同じ幅で2回目の推論は幅64で81 ms、幅128で135 msだった。対応するCPU版は118 ms、245 ms。幅を変えると再準備に約1.7～2.1秒かかり、初回は16.2秒かかった。幅193ではOpenCLのイメージ割り当てに失敗し、試験プロセスが終了した。実稼働の切り出しは幅が複数あるため、PP-OCRv6にはINT8 CPUを使う。

既設SNPEの `isRuntimeAvailable` はCPU、GPU、GPU FP16を利用可能、Hexagonを利用不可と返した。公開QAIRT 2.40のQCS610向けDSPバックエンドを所有者用の一時領域に配置して、量子化ReLU単体の実行を試した。応答が得られず、ADB再接続時の稼働時間から実機の再起動を確認した。再起動の原因とDSP側の失敗箇所は特定できていない。純正5サービスと認識サービスは復帰し、前方フレームの更新と録画継続を確認した。[QAIRTの対応機種・環境](https://docs.qualcomm.com/nav/home/QNN_general_overview.html?product=924033590759186372)

## 追加の高速化

INT8版に渡す切り出しの最大幅を128ピクセルにし、ONNX Runtimeの出力テンソルをそのまま復号に使う。浮動小数点版への切替時は従来の最大幅512ピクセルを使う。既存の記録しきい値は同じ。

録画14枚と合成3枚を同じNP1でCPU 150%・メモリ384 MiB制限にして比較した。陽性16枚の中央値は1,773 msから1,496 msへ15.6%短縮。ONNX推論の中央値は1,352 msから1,159 ms。全フレームの最大値は3,292 msから3,263 msだった。17枚すべてで地名・分類番号・かな・一連番号、既定の記録対象と即時記録の判定が一致した。幅の変更による影響を評価した範囲はこの再生入力に限られる。

常時稼働構成では、取得の目標を2 fpsから4 fpsへ引き上げ、純正カメラが更新したフレームをGPUで検出・読取りする。認識サービス専用バイナリは `tools/device/deploy-plate-engine.py --serial SERIAL --worker WORKER` で適用し、復旧には同じツールの `--rollback` を使う。

既設のTFLiteモデルで稼働するNP1に適用した。配備前は2.00 fps、配備後は処理時刻が異なる5件の状態取得で中央値3.99 fpsだった。検出・読取りの実行先はAdreno。稼働バイナリのハッシュ、純正5サービスの状態と起動ID、前方・車内JPEG、SD録画のRange読み出し、ナンバー画面の実機テスト2件を確認した。

## 計測コード

GPUとCPUの単体試験は [`tools/plate/probes/`](../tools/plate/probes) のプローブをAArch64 Linux向けにビルドし、[`tools/device/bench-plate-accelerator.py`](../tools/device/bench-plate-accelerator.py) で実行した。データはリトルエンディアンで、入力幅の32ビット整数と、`1×3×48×幅` の正規化済みfloat32を1組として連結する。MNNモデルには `mnnconvert -f ONNX --modelFile INPUT.onnx --MNNModel OUTPUT.mnn --bizCode NP1 --fp16` を使用した。GPU試験では `--mode buffer` が無調整のバッファ方式、`--mode image` が調整・コマンド記録を使う画像方式を選ぶ。CPU単体試験では `--mode cpu --library libonnxruntime.so.1` を指定する。
