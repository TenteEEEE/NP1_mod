#include <onnxruntime_cxx_api.h>

#include <array>
#include <chrono>
#include <cstdio>
#include <fstream>
#include <vector>

int main(int argc, char** argv) {
    if (argc != 6) return 2;
    std::ifstream inputs(argv[2], std::ios::binary);
    std::ofstream results(argv[3], std::ios::binary);
    if (!inputs || !results) return 3;
    Ort::Env env(ORT_LOGGING_LEVEL_WARNING, "crop-bench");
    Ort::SessionOptions options;
    options.SetIntraOpNumThreads(2);
    options.SetInterOpNumThreads(1);
    options.SetGraphOptimizationLevel(GraphOptimizationLevel::ORT_ENABLE_ALL);
    Ort::Session session(env, argv[1], options);
    Ort::AllocatorWithDefaultOptions allocator;
    const auto input_name = session.GetInputNameAllocated(0, allocator);
    const auto output_name = session.GetOutputNameAllocated(0, allocator);
    const char* in[] = {input_name.get()};
    const char* out[] = {output_name.get()};
    auto memory = Ort::MemoryInfo::CreateCpu(OrtArenaAllocator, OrtMemTypeDefault);
    int width;
    while (inputs.read(reinterpret_cast<char*>(&width), sizeof(width))) {
        if (width < 8 || width > 512) return 4;
        std::vector<float> crop(3 * 48 * width);
        if (!inputs.read(reinterpret_cast<char*>(crop.data()), crop.size() * sizeof(float))) return 5;
        std::array<int64_t, 4> shape{1, 3, 48, width};
        auto tensor = Ort::Value::CreateTensor<float>(memory, crop.data(), crop.size(), shape.data(), shape.size());
        auto start = std::chrono::steady_clock::now();
        auto inferred = session.Run(Ort::RunOptions{nullptr}, in, &tensor, 1, out, 1);
        double ms = std::chrono::duration<double, std::milli>(std::chrono::steady_clock::now() - start).count();
        const auto& value = inferred[0];
        std::printf("width=%d run=%.3f backend=INT8_CPU\n", width, ms);
        std::fflush(stdout);
        results.write(reinterpret_cast<const char*>(value.GetTensorData<float>()),
                      value.GetTensorTypeAndShapeInfo().GetElementCount() * sizeof(float));
    }
    return results ? 0 : 6;
}
