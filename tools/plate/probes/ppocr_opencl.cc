#include <MNN/Interpreter.hpp>

#include <algorithm>
#include <array>
#include <chrono>
#include <cstdio>
#include <cstring>
#include <memory>
#include <vector>

using Clock = std::chrono::steady_clock;

double ms(Clock::time_point start) {
    return std::chrono::duration<double, std::milli>(Clock::now() - start).count();
}

int main(int argc, char** argv) {
    if (argc != 6) return 2;
    std::unique_ptr<MNN::Interpreter> model(MNN::Interpreter::createFromFile(argv[1]));
    if (!model) return 3;
    std::FILE* entries = std::fopen(argv[2], "rb");
    std::FILE* results = std::fopen(argv[3], "wb");
    if (!entries || !results) return 4;
    const bool gpu = std::strcmp(argv[4], "cpu") != 0;
    MNN::BackendConfig backend;
    backend.precision = std::strcmp(argv[5], "fp16") == 0 ? MNN::BackendConfig::Precision_Low : MNN::BackendConfig::Precision_High;
    MNN::ScheduleConfig config;
    config.type = gpu ? MNN_FORWARD_OPENCL : MNN_FORWARD_CPU;
    config.backupType = MNN_FORWARD_CPU;
    const bool image = std::strcmp(argv[4], "image") == 0;
    config.numThread = 2;
    if (gpu)
        config.numThread = image ? (MNN_GPU_TUNING_FAST | MNN_GPU_RECORD_BATCH | MNN_GPU_MEMORY_IMAGE)
                                 : (MNN_GPU_TUNING_NONE | MNN_GPU_MEMORY_BUFFER);
    config.backendConfig = &backend;
    model->setSessionMode(MNN::Interpreter::Session_Resize_Defer);
    auto* session = model->createSession(config);
    if (!session) return 5;
    auto* input = model->getSessionInput(session, nullptr);
    if (!input) return 6;
    // Each crop starts with its width, followed by its planar FP32 pixels.
    int width;
    int current = 0;
    while (std::fread(&width, sizeof(width), 1, entries) == 1) {
        if (width < 8 || width > 512) return 7;
        std::vector<float> v(3 * 48 * width);
        if (std::fread(v.data(), sizeof(float), v.size(), entries) != v.size()) return 8;
        auto start = Clock::now();
        if (current != width) {
            model->resizeTensor(input, 1, 3, 48, width);
            model->resizeSession(session);
            current = width;
        }
        double resize = ms(start);
        auto* output = model->getSessionOutput(session, nullptr);
        if (!output || output->dimensions() != 3 || output->length(0) != 1 ||
            output->length(1) <= 0 || output->length(2) != 18710) return 9;
        MNN::Tensor host_input(input, MNN::Tensor::CAFFE);
        MNN::Tensor host_output(output, MNN::Tensor::CAFFE);
        std::copy(v.begin(), v.end(), host_input.host<float>());
        start = Clock::now();
        input->copyFromHostTensor(&host_input);
        if (model->runSession(session) != MNN::NO_ERROR) return 10;
        output->copyToHostTensor(&host_output);
        double run = ms(start);
        std::array<int, 3> engines{-1, -1, -1};
        model->getSessionInfo(session, MNN::Interpreter::BACKENDS, engines.data());
        float megabytes = 0;
        model->getSessionInfo(session, MNN::Interpreter::MEMORY, &megabytes);
        std::printf("width=%d resize=%.3f run=%.3f backend=%d,%d memory=%.1f\n", width, resize, run, engines[0], engines[1], megabytes);
        std::fflush(stdout);
        if (std::fwrite(host_output.host<float>(), sizeof(float), host_output.elementSize(), results) != static_cast<size_t>(host_output.elementSize())) return 11;
    }
    std::fclose(entries);
    return std::fclose(results) == 0 ? 0 : 12;
}
