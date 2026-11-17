// NP1 plate-recognition inference helper.
//
// Protocol (see backend/internal/platesensor/engine.go):
//   stdin : "NPF1" | u32 width | u32 height | u32 format(1=NV12) | u64 seq | pixels
//   stdout: one JSON line per frame. The first line is the ready line.
// Diagnostics go to stderr only; stdout carries protocol lines exclusively.
//
// Stage 1: plate detector (YOLO-style, NHWC RGB input, output [1,4+C,N] or [1,N,4+C]).
// Stage 2: plate reader, a multi-head classifier over the fixed Japanese plate
//          layout (region / class digits / kana / serial digits).
#include <algorithm>
#include <array>
#include <chrono>
#include <cmath>
#include <cstdint>
#include <cstdio>
#include <cstring>
#include <filesystem>
#include <fstream>
#include <functional>
#include <limits>
#include <map>
#include <memory>
#include <regex>
#include <set>
#include <stdexcept>
#include <string>
#include <unordered_map>
#include <utility>
#include <vector>

#include <nlohmann/json.hpp>
#include <onnxruntime_cxx_api.h>

#include "tensorflow/lite/interpreter.h"
#include "tensorflow/lite/kernels/register.h"
#include "tensorflow/lite/model.h"

using json = nlohmann::json;

namespace {

struct Box {
  float x, y, w, h, score;
};

struct Head {
  std::string field;
  std::vector<std::string> labels;
};

struct Config {
  float score = 0.4f, iou = 0.5f, margin = 0.06f, field_threshold = 0.6f;
  float region_threshold = 0.6f, kana_threshold = 0.6f;
  int max_plates = 4, min_width = 24;
  int roi[4] = {0, 0, 1920, 1080};
  std::vector<Head> heads;
  std::string model_dir;
  json field_readers = json::object();
  json text_ocr;
};

uint8_t clamp8(int v) { return static_cast<uint8_t>(v < 0 ? 0 : (v > 255 ? 255 : v)); }

// Video-range BT.601 NV12 sample -> full-range RGB.
inline void nv12_rgb(const uint8_t* f, int W, int H, int x, int y, uint8_t* rgb) {
  x = std::clamp(x, 0, W - 1);
  y = std::clamp(y, 0, H - 1);
  const uint8_t* uv = f + W * H + (y / 2) * W + (x & ~1);
  int c = f[y * W + x] - 16, d = uv[0] - 128, e = uv[1] - 128;
  rgb[0] = clamp8((298 * c + 409 * e + 128) >> 8);
  rgb[1] = clamp8((298 * c - 100 * d - 208 * e + 128) >> 8);
  rgb[2] = clamp8((298 * c + 516 * d + 128) >> 8);
}

inline uint8_t nv12_gray(const uint8_t* f, int W, int H, float fx, float fy) {
  // Bilinear on luma, expanded to full range.
  fx = std::clamp(fx, 0.f, W - 1.001f);
  fy = std::clamp(fy, 0.f, H - 1.001f);
  int x0 = static_cast<int>(fx), y0 = static_cast<int>(fy);
  float ax = fx - x0, ay = fy - y0;
  const uint8_t* p = f + y0 * W + x0;
  float v = (p[0] * (1 - ax) + p[1] * ax) * (1 - ay) + (p[W] * (1 - ax) + p[W + 1] * ax) * ay;
  return clamp8(static_cast<int>(std::lround((v - 16.f) * 255.f / 219.f)));
}

// Writes a normalized [0,1] value into an input tensor of any supported type.
struct InputWriter {
  TfLiteTensor* t;
  void set(size_t i, float v) const {
    switch (t->type) {
      case kTfLiteFloat32: t->data.f[i] = v; break;
      case kTfLiteUInt8: {
        auto q = t->params;
        t->data.uint8[i] = q.scale > 0 ? clamp8(static_cast<int>(std::lround(v / q.scale + q.zero_point))) : clamp8(static_cast<int>(v * 255));
        break;
      }
      case kTfLiteInt8: {
        auto q = t->params;
        int x = static_cast<int>(std::lround(v / q.scale + q.zero_point));
        t->data.int8[i] = static_cast<int8_t>(std::clamp(x, -128, 127));
        break;
      }
      default: throw std::runtime_error("unsupported input tensor type");
    }
  }
};

float read_out(const TfLiteTensor* t, size_t i) {
  switch (t->type) {
    case kTfLiteFloat32: return t->data.f[i];
    case kTfLiteUInt8: return (t->data.uint8[i] - t->params.zero_point) * t->params.scale;
    case kTfLiteInt8: return (t->data.int8[i] - t->params.zero_point) * t->params.scale;
    default: throw std::runtime_error("unsupported output tensor type");
  }
}

std::unique_ptr<tflite::Interpreter> load(const std::string& path, int threads,
                                          std::unique_ptr<tflite::FlatBufferModel>& model) {
  model = tflite::FlatBufferModel::BuildFromFile(path.c_str());
  if (!model) throw std::runtime_error("cannot load model " + path);
  tflite::ops::builtin::BuiltinOpResolver resolver;
  std::unique_ptr<tflite::Interpreter> interp;
  tflite::InterpreterBuilder builder(*model, resolver);
  builder.SetNumThreads(threads);
  if (builder(&interp) != kTfLiteOk || !interp) throw std::runtime_error("cannot build interpreter for " + path);
  if (interp->AllocateTensors() != kTfLiteOk) throw std::runtime_error("cannot allocate tensors for " + path);
  return interp;
}

std::vector<float> softmax(const TfLiteTensor* tensor, size_t offset, size_t count) {
  std::vector<float> values(count);
  float maximum = -std::numeric_limits<float>::infinity();
  for (size_t i = 0; i < count; ++i) {
    values[i] = read_out(tensor, offset + i);
    if (!std::isfinite(values[i])) return std::vector<float>(count);
    maximum = std::max(maximum, values[i]);
  }
  double total = 0;
  for (float& value : values) {
    value = std::exp(value - maximum);
    total += value;
  }
  if (!(total > 0)) return std::vector<float>(count);
  for (float& value : values) value = static_cast<float>(value / total);
  return values;
}

std::vector<std::string> utf8_tokens(const std::string& value) {
  std::vector<std::string> out;
  for (size_t at = 0; at < value.size();) {
    unsigned char first = static_cast<unsigned char>(value[at]);
    size_t length = first < 0x80 ? 1 : (first & 0xe0) == 0xc0 ? 2 : (first & 0xf0) == 0xe0 ? 3 : 4;
    if (at + length > value.size()) throw std::runtime_error("invalid UTF-8 model metadata");
    out.push_back(value.substr(at, length));
    at += length;
  }
  return out;
}

std::string join_tokens(const std::vector<std::string>& values, size_t first = 0, size_t last = std::string::npos) {
  if (last == std::string::npos) last = values.size();
  std::string out;
  for (size_t i = first; i < std::min(last, values.size()); ++i) out += values[i];
  return out;
}

std::string canonical_token(std::string token, bool kana = false) {
  static const std::unordered_map<std::string, std::string> aliases = {
      {"户", "戸"}, {"宫", "宮"}, {"滨", "浜"}, {"濱", "浜"}, {"广", "広"},
      {"岛", "島"}, {"长", "長"}, {"泽", "沢"}, {"冈", "岡"}, {"丰", "豊"},
      {"习", "習"}, {"仓", "倉"}, {"贺", "賀"}, {"爱", "愛"}, {"张", "張"}};
  if (kana && (token == "<" || token == "〈" || token == "＜")) return "く";
  auto found = aliases.find(token);
  if (found != aliases.end()) return found->second;
  // NFKC for the characters used by the plate parser: full-width ASCII.
  auto code = utf8_tokens(token);
  if (code.size() == 1 && token.size() == 3) {
    const unsigned char* p = reinterpret_cast<const unsigned char*>(token.data());
    unsigned int cp = ((p[0] & 0x0f) << 12) | ((p[1] & 0x3f) << 6) | (p[2] & 0x3f);
    if (cp >= 0xff01 && cp <= 0xff5e) return std::string(1, static_cast<char>(cp - 0xfee0));
  }
  return token;
}

bool is_digit_token(const std::string& value) { return value.size() == 1 && value[0] >= '0' && value[0] <= '9'; }
bool is_class_token(const std::string& value) {
  return is_digit_token(value) || (value.size() == 1 && std::string("ACFHKLMPXY").find(value[0]) != std::string::npos);
}
bool is_dot_token(const std::string& value) {
  return value == "." || value == "・" || value == "·" || value == "●" || value == "。";
}
bool is_hyphen_token(const std::string& value) { return value == "-" || value == "－" || value == "‐"; }

struct Choice {
  std::string text;
  float confidence = 0;
  double log_probability = -std::numeric_limits<double>::infinity();
};

bool valid_region(const std::string& value, const std::set<std::string>& regions) { return regions.count(value) != 0; }
bool valid_kana(const std::string& value, const std::set<std::string>& kana) { return kana.count(value) != 0; }
bool valid_classification(const std::string& value) {
  if (value.empty() || value.size() > 3 || !is_digit_token(value.substr(0, 1))) return false;
  if (value.size() <= 2) return std::all_of(value.begin(), value.end(), [](char c) { return c >= '0' && c <= '9'; });
  return std::all_of(value.begin(), value.end(), [](char c) {
    return (c >= '0' && c <= '9') || std::string("ACFHKLMPXY").find(c) != std::string::npos;
  });
}
bool valid_serial(const std::string& value) {
  return !value.empty() && value.size() <= 4 && value[0] >= '1' && value[0] <= '9' &&
         std::all_of(value.begin(), value.end(), [](char c) { return c >= '0' && c <= '9'; });
}

Choice single_choice(const std::vector<float>& probabilities, const std::vector<std::string>& labels,
                     const std::function<bool(const std::string&)>& allowed) {
  int best = -1;
  for (size_t i = 0; i < labels.size(); ++i)
    if (allowed(labels[i]) && (best < 0 || probabilities[i] > probabilities[best])) best = static_cast<int>(i);
  if (best < 0) return {};
  float p = probabilities[best];
  return {labels[best], p, std::log(std::max(1e-30f, p))};
}

float label_probability(const std::vector<float>& probabilities, const std::vector<std::string>& labels,
                        const std::string& label) {
  for (size_t i = 0; i < labels.size(); ++i)
    if (labels[i] == label) return probabilities[i];
  return 0;
}

Choice sequence_choice(const std::string& text, const std::vector<std::string>& tokens,
                       const std::vector<std::vector<float>>& probabilities,
                       const std::vector<std::vector<std::string>>& labels) {
  float confidence = 1;
  double score = 0;
  for (size_t i = 0; i < tokens.size(); ++i) {
    float p = label_probability(probabilities[i], labels[i], tokens[i]);
    confidence = std::min(confidence, p);
    score += std::log(std::max(1e-30f, p));
  }
  return {text, confidence, score};
}

std::string best_token(const std::vector<float>& probabilities, const std::vector<std::string>& labels,
                       const std::string& allowed) {
  return single_choice(probabilities, labels, [&](const std::string& value) {
           return value.size() == 1 && allowed.find(value) != std::string::npos;
         }).text;
}

Choice serial_choice(const std::vector<std::vector<float>>& probabilities,
                     const std::vector<std::vector<std::string>>& labels) {
  Choice best;
  for (int length = 1; length <= 4; ++length) {
    std::vector<std::string> tokens(4);
    std::string number;
    int first = 4 - length;
    for (int i = first; i < 4; ++i) {
      tokens[i] = best_token(probabilities[i], labels[i], i == first ? "123456789" : "0123456789");
      number += tokens[i];
    }
    if (!valid_serial(number)) continue;
    Choice candidate = sequence_choice(number, tokens, probabilities, labels);
    if (candidate.log_probability > best.log_probability) best = candidate;
  }
  return best;
}

Choice classification_choice(const std::vector<std::vector<float>>& probabilities,
                              const std::vector<std::vector<std::string>>& labels) {
  Choice best;
  for (int length = 1; length <= 3; ++length) {
    std::vector<std::string> tokens(3);
    std::string value;
    for (int i = 0; i < length; ++i) {
      tokens[i] = best_token(probabilities[i], labels[i], i == 0 || length < 3 ? "0123456789" : "0123456789ACFHKLMPXY");
      value += tokens[i];
    }
    if (!valid_classification(value)) continue;
    Choice candidate = sequence_choice(value, tokens, probabilities, labels);
    if (candidate.log_probability > best.log_probability) best = candidate;
  }
  return best;
}

double log_add(double a, double b) {
  if (a == -std::numeric_limits<double>::infinity()) return b;
  if (b == -std::numeric_limits<double>::infinity()) return a;
  double maximum = std::max(a, b);
  return maximum + std::log1p(std::exp(-std::abs(a - b)));
}

struct LexMatch {
  std::string text;
  double likelihood = 0;
  double posterior = 0;
};

struct OCRText {
  std::vector<std::string> tokens;
  std::vector<float> scores;
  std::string value() const { return join_tokens(tokens); }
};

class CtcLexicon {
 public:
  CtcLexicon() = default;
  CtcLexicon(const std::vector<std::string>& dictionary, std::vector<std::string> names, bool kana) {
    names.erase(std::remove(names.begin(), names.end(), ""), names.end());
    std::sort(names.begin(), names.end());
    names_ = std::move(names);
    std::set<std::string> needed;
    for (const auto& name : names_)
      for (auto token : utf8_tokens(name)) needed.insert(canonical_token(token, kana));
    std::map<std::string, std::vector<int>> indices;
    for (size_t i = 1; i < dictionary.size(); ++i) {
      auto tokens = utf8_tokens(dictionary[i]);
      if (tokens.size() != 1) continue;
      std::string token = canonical_token(tokens[0], kana);
      if (needed.count(token)) indices[token].push_back(static_cast<int>(i));
    }
    parents_.push_back(0);
    characters_.push_back("");
    std::map<std::string, int> prefixes{{"", 0}};
    for (const auto& name : names_) {
      int at = 0;
      std::string prefix;
      for (auto token : utf8_tokens(name)) {
        token = canonical_token(token, kana);
        prefix += token;
        auto found = prefixes.find(prefix);
        if (found == prefixes.end()) {
          int next = static_cast<int>(parents_.size());
          prefixes[prefix] = next;
          parents_.push_back(at);
          characters_.push_back(token);
          at = next;
        } else {
          at = found->second;
        }
      }
      terminals_.push_back(at);
    }
    std::map<std::string, int> unique;
    emission_index_.resize(parents_.size());
    for (size_t i = 1; i < parents_.size(); ++i) {
      auto inserted = unique.emplace(characters_[i], static_cast<int>(unique.size()));
      emission_index_[i] = inserted.first->second;
    }
    emission_ids_.resize(unique.size());
    for (const auto& entry : unique) emission_ids_[entry.second] = indices[entry.first];
  }

  LexMatch score(const float* probabilities, int steps, int count) const {
    if (steps < 1 || count < 1 || names_.empty()) return {};
    const double negative = -std::numeric_limits<double>::infinity();
    std::vector<double> emissions(emission_ids_.size() * steps, negative), blanks(steps);
    for (size_t c = 0; c < emission_ids_.size(); ++c) {
      for (int t = 0; t < steps; ++t) {
        double sum = 0;
        for (int id : emission_ids_[c]) if (id < count) sum += probabilities[t * count + id];
        emissions[c * steps + t] = std::log(std::max(1e-30, std::min(1.0, sum)));
      }
    }
    for (int t = 0; t < steps; ++t)
      blanks[t] = std::log(std::max(1e-30f, probabilities[t * count]));
    std::vector<double> previous_blank(parents_.size(), negative), previous_label(parents_.size(), negative);
    std::vector<double> next_blank(parents_.size(), negative), next_label(parents_.size(), negative);
    previous_blank[0] = blanks[0];
    for (size_t i = 1; i < parents_.size(); ++i)
      if (parents_[i] == 0) previous_label[i] = emissions[emission_index_[i] * steps];
    for (int t = 1; t < steps; ++t) {
      next_blank[0] = previous_blank[0] + blanks[t];
      next_label[0] = negative;
      for (size_t i = 1; i < parents_.size(); ++i) {
        int parent = parents_[i];
        next_blank[i] = log_add(previous_blank[i], previous_label[i]) + blanks[t];
        double value = log_add(previous_label[i], previous_blank[parent]);
        if (parent != 0 && characters_[i] != characters_[parent]) value = log_add(value, previous_label[parent]);
        next_label[i] = value + emissions[emission_index_[i] * steps + t];
      }
      previous_blank.swap(next_blank);
      previous_label.swap(next_label);
    }
    double best = negative, total = negative;
    std::string selected;
    for (size_t word = 0; word < names_.size(); ++word) {
      int node = terminals_[word];
      double probability = log_add(previous_blank[node], previous_label[node]);
      total = log_add(total, probability);
      if (probability > best) {
        best = probability;
        selected = names_[word];
      }
    }
    if (selected.empty()) return {};
    return {selected, std::exp(best / std::max<size_t>(1, utf8_tokens(selected).size())), std::exp(best - total)};
  }

 private:
  std::vector<std::string> names_, characters_;
  std::vector<int> parents_, terminals_, emission_index_;
  std::vector<std::vector<int>> emission_ids_;
};

void fill_reader_input(tflite::Interpreter* interpreter, const uint8_t* frame, int W, int H,
                       const Box& box, const std::array<double, 4>& rect) {
  TfLiteTensor* tensor = interpreter->tensor(interpreter->inputs()[0]);
  int ih = tensor->dims->data[1], iw = tensor->dims->data[2], channels = tensor->dims->data[3];
  if (channels != 1 && channels != 3) throw std::runtime_error("aux reader channels must be 1 or 3");
  float x0 = box.x + static_cast<float>(rect[0] * box.w);
  float y0 = box.y + static_cast<float>(rect[1] * box.h);
  float cw = static_cast<float>((rect[2] - rect[0]) * box.w);
  float ch = static_cast<float>((rect[3] - rect[1]) * box.h);
  InputWriter writer{tensor};
  uint8_t rgb[3];
  for (int y = 0; y < ih; ++y) {
    for (int x = 0; x < iw; ++x) {
      float fx = x0 + (x + .5f) * cw / iw, fy = y0 + (y + .5f) * ch / ih;
      size_t at = (static_cast<size_t>(y) * iw + x) * channels;
      if (channels == 1) {
        writer.set(at, nv12_gray(frame, W, H, fx, fy) / 255.f);
      } else {
        nv12_rgb(frame, W, H, static_cast<int>(fx), static_cast<int>(fy), rgb);
        for (int c = 0; c < 3; ++c) writer.set(at + c, rgb[c] / 255.f);
      }
    }
  }
}

class AuxReader {
 public:
  AuxReader(const std::string& path, const json& config, std::vector<Head> heads, int threads)
      : heads_(std::move(heads)) {
    interpreter_ = load(path, threads, model_);
    auto values = config.at("rect").get<std::vector<double>>();
    if (values.size() != 4) throw std::runtime_error("aux reader rect must contain four values");
    std::copy(values.begin(), values.end(), rect_.begin());
    size_t expected = 0;
    for (const auto& head : heads_) expected += head.labels.size();
    const TfLiteTensor* output = interpreter_->tensor(interpreter_->outputs()[0]);
    size_t actual = 1;
    for (int i = 0; i < output->dims->size; ++i) actual *= output->dims->data[i];
    if (actual != expected) throw std::runtime_error("aux reader output size does not match configured heads");
  }

  Choice read(const uint8_t* frame, int W, int H, const Box& box) {
    fill_reader_input(interpreter_.get(), frame, W, H, box, rect_);
    if (interpreter_->Invoke() != kTfLiteOk) throw std::runtime_error("aux reader invoke failed");
    const TfLiteTensor* output = interpreter_->tensor(interpreter_->outputs()[0]);
    std::vector<std::vector<float>> probabilities;
    std::vector<std::vector<std::string>> labels;
    size_t offset = 0;
    for (const auto& head : heads_) {
      probabilities.push_back(softmax(output, offset, head.labels.size()));
      labels.push_back(head.labels);
      offset += head.labels.size();
    }
    if (heads_.size() == 1 && heads_[0].field == "kana")
      return single_choice(probabilities[0], labels[0], [](const std::string& value) {
        static const std::string kana = "あいうえかきくけこをさすせそたちつてとなにぬねのはひふほまみむめもやゆよらりるろわれ";
        return !value.empty() && kana.find(value) != std::string::npos;
      });
    return classification_choice(probabilities, labels);
  }

 private:
  std::array<double, 4> rect_{};
  std::vector<Head> heads_;
  std::unique_ptr<tflite::FlatBufferModel> model_;
  std::unique_ptr<tflite::Interpreter> interpreter_;
};

class TextOCR {
 public:
  TextOCR(const std::string& model_path, const std::string& dictionary_path, const json& config,
          const std::vector<std::string>& regions, const std::vector<std::string>& kana)
      : regions_(regions.begin(), regions.end()), kana_(kana.begin(), kana.end()) {
    std::ifstream dictionary_file(dictionary_path);
    if (!dictionary_file) throw std::runtime_error("cannot open PP-OCR dictionary");
    dictionary_ = json::parse(dictionary_file).get<std::vector<std::string>>();
    region_threshold_ = config.value("regionThreshold", .6);
    kana_threshold_ = config.value("kanaThreshold", .6);
    night_fallback_ = config.value("nightFallback", false);
    env_ = std::make_unique<Ort::Env>(ORT_LOGGING_LEVEL_WARNING, "np1-plate");
    Ort::SessionOptions options;
    options.SetIntraOpNumThreads(std::clamp(config.value("cpuThreads", 2), 1, 4));
    options.SetInterOpNumThreads(1);
    options.SetGraphOptimizationLevel(GraphOptimizationLevel::ORT_ENABLE_ALL);
    session_ = std::make_unique<Ort::Session>(*env_, model_path.c_str(), options);
    Ort::AllocatorWithDefaultOptions allocator;
    input_name_ = session_->GetInputNameAllocated(0, allocator).get();
    output_name_ = session_->GetOutputNameAllocated(0, allocator).get();
    region_lexicon_ = CtcLexicon(dictionary_, regions, false);
    kana_lexicon_ = CtcLexicon(dictionary_, kana, true);
  }

  json refine(const uint8_t* frame, int W, int H, const Box& box, const Choice& base_region,
              const Choice& base_class, const Choice& base_kana, Choice base_serial,
              const Choice& trained_kana, const Choice& trained_class) {
    static const std::vector<std::array<double, 4>> region_rects{{{.194, 0, .519, .325}}, {{.15, 0, .53, .37}}, {{.12, 0, .56, .42}}};
    static const std::vector<std::array<double, 4>> kana_rects{{{.02, .52, .195, .84}}, {{.015, .47, .215, .88}}, {{.04, .42, .18, .95}}};
    LexMatch region_match = lex(frame, W, H, box, region_rects, region_lexicon_, "", region_threshold_);
    LexMatch kana_match = lex(frame, W, H, box, kana_rects, kana_lexicon_, base_kana.confidence >= .6f ? base_kana.text : "", kana_threshold_);
    std::string region = region_match.likelihood >= region_threshold_ ? region_match.text : "";
    std::string classification = base_class.confidence >= .6f ? base_class.text : "";
    std::string kana = base_kana.confidence >= .6f ? base_kana.text : "";
    std::string serial = base_serial.text;
    float serial_confidence = base_serial.confidence;
    if (kana_match.likelihood >= kana_threshold_) kana = kana_match.text;
    else if (!kana.empty() && kana_match.likelihood >= .6 && kana_match.text != kana) kana.clear();
    else if (kana.empty() && trained_kana.confidence >= .98f && trained_kana.text == base_kana.text) kana = trained_kana.text;
    if (classification.empty() && trained_class.confidence >= .98f && trained_class.text == base_class.text)
      classification = trained_class.text;
    Candidate number = number_candidate(frame, W, H, box, {.195, .34, 1, 1});
    if (number.score >= .98f) {
      if (serial_confidence < .6f && number.text.size() == base_serial.text.size()) {
        serial = number.text;
        serial_confidence = number.score;
      } else if (serial_confidence >= .6f && number.text != serial) {
        Candidate second = number_candidate(frame, W, H, box, {.215, .38, 1, .98});
        if (second.score >= .98f && second.text == number.text && number.text.size() == base_serial.text.size()) {
          serial = number.text;
          serial_confidence = std::min(number.score, second.score);
        } else serial_confidence = 0;
      }
    }
    if (night_fallback_) {
      if (region.empty() && !classification.empty()) {
        region = region_context(frame, W, H, box, classification, {{{.14, .055, .92, .365}}, {{.08, 0, .94, .4}}});
        if (region.empty()) region = region_context(frame, W, H, box, classification, {{{.08, 0, .95, .48}}, {{.08, 0, .95, .55}}});
      }
      if (kana.empty() && serial_confidence >= .6f)
        kana = kana_context(frame, W, H, box, serial, {{{0, .34, 1, 1}}, {{.015, .40, .99, .98}}});
    }
    return {{"region", region}, {"class", classification}, {"kana", kana}, {"number", serial}, {"confidence", serial_confidence}};
  }

 private:
  struct Candidate { std::string text; float score = 0; };

  std::vector<float> infer(const uint8_t* frame, int W, int H, const Box& box,
                           const std::array<double, 4>& rect, int& steps) {
    int x0 = std::max(0, static_cast<int>(rect[0] * box.w));
    int y0 = std::max(0, static_cast<int>(rect[1] * box.h));
    int x1 = std::min(static_cast<int>(box.w), std::max(x0 + 1, static_cast<int>(rect[2] * box.w)));
    int y1 = std::min(static_cast<int>(box.h), std::max(y0 + 1, static_cast<int>(rect[3] * box.h)));
    int crop_width = x1 - x0, crop_height = y1 - y0;
    int width = std::clamp(static_cast<int>(std::lround(48.0 * crop_width / crop_height)), 8, 512);
    size_t plane = static_cast<size_t>(48) * width;
    std::vector<float> input(3 * plane);
    uint8_t rgb[3];
    for (int y = 0; y < 48; ++y) {
      float sy = std::clamp((y + .5f) * crop_height / 48.f - .5f, 0.f, crop_height - 1.f);
      for (int x = 0; x < width; ++x) {
        float sx = std::clamp((x + .5f) * crop_width / width - .5f, 0.f, crop_width - 1.f);
        nv12_rgb(frame, W, H, static_cast<int>(box.x) + x0 + static_cast<int>(std::lround(sx)),
                 static_cast<int>(box.y) + y0 + static_cast<int>(std::lround(sy)), rgb);
        for (int c = 0; c < 3; ++c) input[c * plane + static_cast<size_t>(y) * width + x] = rgb[c] / 127.5f - 1.f;
      }
    }
    const std::array<int64_t, 4> shape{1, 3, 48, width};
    auto memory = Ort::MemoryInfo::CreateCpu(OrtArenaAllocator, OrtMemTypeDefault);
    auto tensor = Ort::Value::CreateTensor<float>(memory, input.data(), input.size(), shape.data(), shape.size());
    const char* inputs[] = {input_name_.c_str()};
    const char* outputs[] = {output_name_.c_str()};
    auto result = session_->Run(Ort::RunOptions{nullptr}, inputs, &tensor, 1, outputs, 1);
    auto info = result[0].GetTensorTypeAndShapeInfo();
    auto output_shape = info.GetShape();
    if (output_shape.size() != 3 || output_shape[0] != 1 || output_shape[2] != static_cast<int64_t>(dictionary_.size()))
      throw std::runtime_error("PP-OCR output shape does not match dictionary");
    steps = static_cast<int>(output_shape[1]);
    size_t count = info.GetElementCount();
    const float* values = result[0].GetTensorData<float>();
    return std::vector<float>(values, values + count);
  }

  OCRText text(const uint8_t* frame, int W, int H, const Box& box, const std::array<double, 4>& rect) {
    int steps = 0;
    std::vector<float> values = infer(frame, W, H, box, rect, steps);
    OCRText decoded;
    int previous = -1;
    float maximum = 0;
    for (int t = 0; t <= steps; ++t) {
      int index = -1;
      float score = 0;
      if (t < steps) {
        index = 0;
        for (size_t i = 1; i < dictionary_.size(); ++i)
          if (values[t * dictionary_.size() + i] > values[t * dictionary_.size() + index]) index = static_cast<int>(i);
        score = values[t * dictionary_.size() + index];
      }
      if (index != previous) {
        if (previous > 0) {
          for (auto token : utf8_tokens(dictionary_[previous])) {
            token = canonical_token(token);
            if (token != " " && token != "\t" && token != "\n") {
              decoded.tokens.push_back(token);
              decoded.scores.push_back(maximum);
            }
          }
        }
        previous = index;
        maximum = score;
      } else maximum = std::max(maximum, score);
    }
    return decoded;
  }

  LexMatch lex(const uint8_t* frame, int W, int H, const Box& box,
               const std::vector<std::array<double, 4>>& rects, const CtcLexicon& lexicon,
               const std::string& known, double threshold) {
    LexMatch best;
    std::set<std::string> strong;
    for (const auto& rect : rects) {
      int steps = 0;
      std::vector<float> values = infer(frame, W, H, box, rect, steps);
      LexMatch match = lexicon.score(values.data(), steps, static_cast<int>(dictionary_.size()));
      if (match.posterior < .9) continue;
      if (!known.empty() && match.text == known && match.likelihood >= std::max(.6, threshold)) return match;
      if (match.likelihood >= .6) strong.insert(match.text);
      if (match.likelihood > best.likelihood) best = match;
    }
    return strong.size() > 1 ? LexMatch{} : best;
  }

  static bool serial_text(const std::string& value, const std::string& expected) {
    if (!valid_serial(expected)) return false;
    std::string digits;
    for (char c : value) if (c >= '0' && c <= '9') digits.push_back(c);
    if (digits != expected) return false;
    return std::all_of(value.begin(), value.end(), [](unsigned char c) {
      return (c >= '0' && c <= '9') || c == '-' || c == '.' || c >= 0x80;
    });
  }

  std::string region_context(const uint8_t* frame, int W, int H, const Box& box,
                             const std::string& classification,
                             const std::vector<std::array<double, 4>>& rects) {
    auto decode = [&](const std::array<double, 4>& rect) {
      OCRText value = text(frame, W, H, box, rect);
      for (const auto& region : regions_) {
        auto region_tokens = utf8_tokens(region);
        if (value.tokens.size() < region_tokens.size() + classification.size()) continue;
        bool match = true;
        double log_probability = 0;
        for (size_t i = 0; i < region_tokens.size(); ++i) {
          if (canonical_token(value.tokens[i]) != region_tokens[i] || value.scores[i] < .85f) match = false;
          log_probability += std::log(std::max(1e-30f, value.scores[i]));
        }
        if (!match || std::exp(log_probability / region_tokens.size()) < .95) continue;
        for (size_t i = 0; i < classification.size(); ++i) {
          size_t at = region_tokens.size() + i;
          if (value.tokens[at] != classification.substr(i, 1) || value.scores[at] < .98f) match = false;
        }
        if (match) return region;
      }
      return std::string{};
    };
    if (rects.size() < 2) return {};
    std::string first = decode(rects[0]);
    return !first.empty() && first == decode(rects[1]) ? first : "";
  }

  std::string kana_context(const uint8_t* frame, int W, int H, const Box& box, const std::string& serial,
                           const std::vector<std::array<double, 4>>& rects) {
    auto decode = [&](const std::array<double, 4>& rect) {
      OCRText value = text(frame, W, H, box, rect);
      if (value.tokens.empty()) return std::string{};
      std::string first = canonical_token(value.tokens[0], true);
      if (!kana_.count(first) || value.scores[0] < .95f || !serial_text(join_tokens(value.tokens, 1), serial)) return std::string{};
      for (size_t i = 1; i < value.tokens.size(); ++i)
        if (is_digit_token(value.tokens[i]) && value.scores[i] < .98f) return std::string{};
      return first;
    };
    if (rects.size() < 2) return {};
    std::string first = decode(rects[0]);
    return !first.empty() && first == decode(rects[1]) ? first : "";
  }

  Candidate number_candidate(const uint8_t* frame, int W, int H, const Box& box,
                             const std::array<double, 4>& rect) {
    OCRText value = text(frame, W, H, box, rect);
    Candidate result;
    result.score = 1;
    for (size_t i = 0; i < value.tokens.size(); ++i) {
      if (is_digit_token(value.tokens[i])) {
        result.text += value.tokens[i];
        result.score = std::min(result.score, value.scores[i]);
      } else if (!is_dot_token(value.tokens[i]) && !is_hyphen_token(value.tokens[i])) return {};
    }
    return valid_serial(result.text) ? result : Candidate{};
  }

  std::unique_ptr<Ort::Env> env_;
  std::unique_ptr<Ort::Session> session_;
  std::string input_name_, output_name_;
  std::vector<std::string> dictionary_;
  std::set<std::string> regions_, kana_;
  CtcLexicon region_lexicon_, kana_lexicon_;
  double region_threshold_ = .6, kana_threshold_ = .6;
  bool night_fallback_ = false;
};

float iou(const Box& a, const Box& b) {
  float x1 = std::max(a.x, b.x), y1 = std::max(a.y, b.y);
  float x2 = std::min(a.x + a.w, b.x + b.w), y2 = std::min(a.y + a.h, b.y + b.h);
  float inter = std::max(0.f, x2 - x1) * std::max(0.f, y2 - y1);
  return inter / (a.w * a.h + b.w * b.h - inter + 1e-6f);
}

class Engine {
 public:
  Engine(const std::string& det, const std::string& ocr, const Config& cfg, int threads) : cfg_(cfg) {
    det_ = load(det, threads, det_model_);
    ocr_ = load(ocr, threads, ocr_model_);
    auto* in = det_->tensor(det_->inputs()[0]);
    if (in->dims->size != 4 || in->dims->data[3] != 3) throw std::runtime_error("detector input must be NHWC RGB");
    dh_ = in->dims->data[1];
    dw_ = in->dims->data[2];
    auto* oin = ocr_->tensor(ocr_->inputs()[0]);
    if (oin->dims->size != 4) throw std::runtime_error("reader input must be NHWC");
    oh_ = oin->dims->data[1];
    ow_ = oin->dims->data[2];
    oc_ = oin->dims->data[3];
    if (oc_ != 1 && oc_ != 3) throw std::runtime_error("reader input must have 1 or 3 channels");
    size_t total = 0;
    for (auto& h : cfg_.heads) total += h.labels.size();
    auto* oout = ocr_->tensor(ocr_->outputs()[0]);
    size_t n = 1;
    for (int i = 0; i < oout->dims->size; ++i) n *= oout->dims->data[i];
    if (n != total) throw std::runtime_error("reader output size does not match configured heads");

    std::vector<Head> kana_heads, class_heads;
    std::vector<std::string> regions, kana;
    for (const auto& head : cfg_.heads) {
      if (head.field == "region") regions = head.labels;
      else if (head.field == "kana") { kana = head.labels; kana_heads.push_back(head); }
      else if (head.field == "class") class_heads.push_back(head);
    }
    if (cfg_.field_readers.contains("kana")) {
      auto spec = cfg_.field_readers.at("kana");
      aux_kana_ = std::make_unique<AuxReader>((std::filesystem::path(cfg_.model_dir) / spec.at("file").get<std::string>()).string(),
                                               spec, kana_heads, threads);
    }
    if (cfg_.field_readers.contains("classification")) {
      auto spec = cfg_.field_readers.at("classification");
      aux_class_ = std::make_unique<AuxReader>((std::filesystem::path(cfg_.model_dir) / spec.at("file").get<std::string>()).string(),
                                                spec, class_heads, threads);
    }
    if (!cfg_.text_ocr.is_null() && !cfg_.text_ocr.empty()) {
      auto model = (std::filesystem::path(cfg_.model_dir) / cfg_.text_ocr.at("file").get<std::string>()).string();
      auto dictionary = (std::filesystem::path(cfg_.model_dir) / cfg_.text_ocr.at("dictionary").get<std::string>()).string();
      text_ocr_ = std::make_unique<TextOCR>(model, dictionary, cfg_.text_ocr, regions, kana);
    }
  }

  // Times each stage on a synthetic gray frame (device-side measurement).
  json bench(int n) {
    std::vector<uint8_t> frame(1920 * 1080 * 3 / 2, 128);
    auto time_ms = [&](auto&& fn) {
      fn();  // warm-up
      auto t0 = std::chrono::steady_clock::now();
      for (int i = 0; i < n; ++i) fn();
      return std::chrono::duration<double, std::milli>(std::chrono::steady_clock::now() - t0).count() / n;
    };
    Box b{800, 600, 200, 100, 1};
    double det = time_ms([&] { detect(frame.data(), 1920, 1080); });
    double ocr = time_ms([&] { read(frame.data(), 1920, 1080, b); });
    return {{"detectorMs", det}, {"readerMs", ocr}, {"runs", n}};
  }

  json process(const uint8_t* f, int W, int H) {
    json plates = json::array();
    for (const Box& b : detect(f, W, H)) {
      if (b.w < cfg_.min_width) continue;
      json reading = read(f, W, H, b);
      plates.push_back({{"box", {std::lround(b.x), std::lround(b.y), std::lround(b.w), std::lround(b.h)}},
                        {"score", b.score},
                        {"reading", reading}});
    }
    return plates;
  }

  bool latest_models() const { return aux_kana_ && aux_class_ && text_ocr_; }

 private:
  std::vector<Box> detect(const uint8_t* f, int W, int H) {
    int rx = std::clamp(cfg_.roi[0], 0, W - 2), ry = std::clamp(cfg_.roi[1], 0, H - 2);
    int rw = std::clamp(cfg_.roi[2], 2, W - rx), rh = std::clamp(cfg_.roi[3], 2, H - ry);
    float s = std::min(static_cast<float>(dw_) / rw, static_cast<float>(dh_) / rh);
    float px = (dw_ - rw * s) / 2, py = (dh_ - rh * s) / 2;
    InputWriter in{det_->tensor(det_->inputs()[0])};
    uint8_t rgb[3];
    for (int y = 0; y < dh_; ++y) {
      for (int x = 0; x < dw_; ++x) {
        size_t i = (static_cast<size_t>(y) * dw_ + x) * 3;
        float sx = (x + 0.5f - px) / s, sy = (y + 0.5f - py) / s;
        if (sx < 0 || sy < 0 || sx >= rw || sy >= rh) {
          for (int c = 0; c < 3; ++c) in.set(i + c, 114.f / 255.f);
          continue;
        }
        nv12_rgb(f, W, H, rx + static_cast<int>(sx), ry + static_cast<int>(sy), rgb);
        for (int c = 0; c < 3; ++c) in.set(i + c, rgb[c] / 255.f);
      }
    }
    if (det_->Invoke() != kTfLiteOk) throw std::runtime_error("detector invoke failed");
    const TfLiteTensor* out = det_->tensor(det_->outputs()[0]);
    if (out->dims->size != 3) throw std::runtime_error("detector output must be rank 3");
    int a = out->dims->data[1], b = out->dims->data[2];
    bool channels_first = a < b;  // [1,4+C,N] (ultralytics) vs [1,N,4+C]
    int attrs = channels_first ? a : b, n = channels_first ? b : a;
    if (attrs < 5) throw std::runtime_error("detector output has no class scores");
    auto at = [&](int k, int j) { return read_out(out, channels_first ? static_cast<size_t>(k) * n + j : static_cast<size_t>(j) * attrs + k); };
    std::vector<Box> cand;
    float maxc = 0;
    for (int j = 0; j < n; ++j) {
      float score = 0;
      for (int k = 4; k < attrs; ++k) score = std::max(score, at(k, j));
      if (score < cfg_.score) continue;
      Box bx{at(0, j), at(1, j), at(2, j), at(3, j), score};
      maxc = std::max({maxc, bx.x, bx.y});
      cand.push_back(bx);
    }
    // Ultralytics TFLite exports normalized xywh; scale to input pixels if so.
    bool normalized = maxc <= 1.5f;
    for (auto& bx : cand) {
      if (normalized) { bx.x *= dw_; bx.w *= dw_; bx.y *= dh_; bx.h *= dh_; }
      float x0 = (bx.x - bx.w / 2 - px) / s + rx, y0 = (bx.y - bx.h / 2 - py) / s + ry;
      bx = Box{x0, y0, bx.w / s, bx.h / s, bx.score};
    }
    std::sort(cand.begin(), cand.end(), [](const Box& l, const Box& r) { return l.score > r.score; });
    std::vector<Box> keep;
    for (const auto& bx : cand) {
      bool dup = false;
      for (const auto& k : keep) dup = dup || iou(bx, k) > cfg_.iou;
      if (!dup) keep.push_back(bx);
      if (static_cast<int>(keep.size()) >= cfg_.max_plates) break;
    }
    return keep;
  }

  json read(const uint8_t* f, int W, int H, const Box& b) {
    float mx = b.w * cfg_.margin, my = b.h * cfg_.margin;
    float x0 = b.x - mx, y0 = b.y - my, cw = b.w + 2 * mx, ch = b.h + 2 * my;
    InputWriter in{ocr_->tensor(ocr_->inputs()[0])};
    uint8_t rgb[3];
    for (int y = 0; y < oh_; ++y) {
      for (int x = 0; x < ow_; ++x) {
        float fx = x0 + (x + 0.5f) * cw / ow_, fy = y0 + (y + 0.5f) * ch / oh_;
        size_t i = (static_cast<size_t>(y) * ow_ + x) * oc_;
        if (oc_ == 1) {
          in.set(i, nv12_gray(f, W, H, fx, fy) / 255.f);
        } else {
          nv12_rgb(f, W, H, static_cast<int>(fx), static_cast<int>(fy), rgb);
          for (int c = 0; c < 3; ++c) in.set(i + c, rgb[c] / 255.f);
        }
      }
    }
    if (ocr_->Invoke() != kTfLiteOk) throw std::runtime_error("reader invoke failed");
    const TfLiteTensor* out = ocr_->tensor(ocr_->outputs()[0]);
    std::vector<float> region_probability, kana_probability;
    std::vector<std::string> region_labels, kana_labels;
    std::vector<std::vector<float>> class_probability, number_probability;
    std::vector<std::vector<std::string>> class_labels, number_labels;
    size_t offset = 0;
    for (const auto& h : cfg_.heads) {
      std::vector<float> probabilities = softmax(out, offset, h.labels.size());
      if (h.field == "region") { region_probability = std::move(probabilities); region_labels = h.labels; }
      else if (h.field == "kana") { kana_probability = std::move(probabilities); kana_labels = h.labels; }
      else if (h.field == "class") { class_probability.push_back(std::move(probabilities)); class_labels.push_back(h.labels); }
      else { number_probability.push_back(std::move(probabilities)); number_labels.push_back(h.labels); }
      offset += h.labels.size();
    }
    Choice base_region = single_choice(region_probability, region_labels, [&](const std::string& value) {
      return std::find(region_labels.begin() + std::min<size_t>(1, region_labels.size()), region_labels.end(), value) != region_labels.end();
    });
    Choice base_kana = single_choice(kana_probability, kana_labels, [](const std::string& value) {
      static const std::string kana = "あいうえかきくけこをさすせそたちつてとなにぬねのはひふほまみむめもやゆよらりるろわれ";
      return !value.empty() && kana.find(value) != std::string::npos;
    });
    Choice base_class = classification_choice(class_probability, class_labels);
    Choice base_serial = serial_choice(number_probability, number_labels);
    Choice trained_kana = aux_kana_ ? aux_kana_->read(f, W, H, b) : Choice{};
    Choice trained_class = aux_class_ ? aux_class_->read(f, W, H, b) : Choice{};
    if (text_ocr_)
      return text_ocr_->refine(f, W, H, b, base_region, base_class, base_kana, base_serial, trained_kana, trained_class);
    return {{"region", base_region.confidence >= cfg_.field_threshold ? base_region.text : ""},
            {"class", base_class.confidence >= cfg_.field_threshold ? base_class.text : ""},
            {"kana", base_kana.confidence >= cfg_.field_threshold ? base_kana.text : ""},
            {"number", base_serial.text}, {"confidence", base_serial.confidence}};
  }

  Config cfg_;
  std::unique_ptr<tflite::FlatBufferModel> det_model_, ocr_model_;
  std::unique_ptr<tflite::Interpreter> det_, ocr_;
  std::unique_ptr<AuxReader> aux_kana_, aux_class_;
  std::unique_ptr<TextOCR> text_ocr_;
  int dw_ = 0, dh_ = 0, ow_ = 0, oh_ = 0, oc_ = 0;
};

Config load_config(const std::string& path) {
  std::ifstream in(path);
  if (!in) throw std::runtime_error("cannot open config");
  json j = json::parse(in);
  if (j.value("version", 0) != 1) throw std::runtime_error("unsupported config version");
  Config c;
  c.model_dir = std::filesystem::path(path).parent_path().string();
  if (j.contains("detector")) {
    auto& d = j["detector"];
    c.score = d.value("scoreThreshold", c.score);
    c.iou = d.value("iouThreshold", c.iou);
    c.max_plates = d.value("maxPlates", c.max_plates);
    c.min_width = d.value("minWidth", c.min_width);
    if (d.contains("roi")) for (int i = 0; i < 4; ++i) c.roi[i] = d["roi"].at(i).get<int>();
  }
  auto& o = j.at("ocr");
  c.margin = o.value("margin", c.margin);
  c.field_threshold = o.value("fieldThreshold", c.field_threshold);
  c.field_readers = o.value("fieldReaders", json::object());
  if (o.contains("textOcr")) {
    c.text_ocr = o.at("textOcr");
    c.region_threshold = c.text_ocr.value("regionThreshold", c.region_threshold);
    c.kana_threshold = c.text_ocr.value("kanaThreshold", c.kana_threshold);
  }
  for (auto& h : o.at("heads")) {
    Head head{h.at("field").get<std::string>(), h.at("labels").get<std::vector<std::string>>()};
    if (head.field != "region" && head.field != "class" && head.field != "kana" && head.field != "number")
      throw std::runtime_error("unknown head field " + head.field);
    if (head.labels.empty()) throw std::runtime_error("empty head");
    c.heads.push_back(head);
  }
  if (c.heads.empty()) throw std::runtime_error("no reader heads");
  return c;
}

bool read_exact(void* p, size_t n) { return std::fread(p, 1, n, stdin) == n; }

void emit(const json& j) {
  std::string s = j.dump(-1, ' ', false, json::error_handler_t::replace);
  s.push_back('\n');
  std::fwrite(s.data(), 1, s.size(), stdout);
  std::fflush(stdout);
}

}  // namespace

int main(int argc, char** argv) {
  std::string det, ocr, conf;
  int threads = 2, bench = 0;
  for (int i = 1; i + 1 < argc; i += 2) {
    std::string k = argv[i], v = argv[i + 1];
    if (k == "--detector") det = v;
    else if (k == "--ocr") ocr = v;
    else if (k == "--config") conf = v;
    else if (k == "--threads") threads = std::clamp(std::atoi(v.c_str()), 1, 4);
    else if (k == "--bench") bench = std::clamp(std::atoi(v.c_str()), 1, 1000);
  }
  std::unique_ptr<Engine> engine;
  try {
    if (det.empty() || ocr.empty() || conf.empty()) throw std::runtime_error("usage: --detector D --ocr O --config C [--threads N]");
    engine = std::make_unique<Engine>(det, ocr, load_config(conf), threads);
  } catch (const std::exception& e) {
    emit({{"ready", false}, {"error", e.what()}});
    return 1;
  }
  emit({{"ready", true}, {"engine", "tflite " TFLITE_VERSION_LABEL " + onnxruntime"},
        {"backend", "xnnpack cpu x" + std::to_string(threads) + " + onnx cpu"},
        {"latestModels", engine->latest_models()}});
  if (bench) {
    emit({{"bench", engine->bench(bench)}});
    return 0;
  }
  std::vector<uint8_t> frame;
  uint8_t header[24];
  while (read_exact(header, sizeof header)) {
    uint32_t w, h, fmt;
    uint64_t seq;
    std::memcpy(&w, header + 4, 4);
    std::memcpy(&h, header + 8, 4);
    std::memcpy(&fmt, header + 12, 4);
    std::memcpy(&seq, header + 16, 8);
    if (std::memcmp(header, "NPF1", 4) != 0 || fmt != 1 || w < 16 || h < 16 || w > 4096 || h > 4096 || (w | h) & 1) {
      std::fprintf(stderr, "invalid frame header\n");
      return 2;  // stream position is unknown; let the supervisor restart us
    }
    frame.resize(static_cast<size_t>(w) * h * 3 / 2);
    if (!read_exact(frame.data(), frame.size())) return 0;
    auto t0 = std::chrono::steady_clock::now();
    json reply = {{"seq", seq}};
    try {
      reply["plates"] = engine->process(frame.data(), static_cast<int>(w), static_cast<int>(h));
    } catch (const std::exception& e) {
      reply["plates"] = json::array();
      reply["error"] = e.what();
    }
    reply["ms"] = std::chrono::duration<double, std::milli>(std::chrono::steady_clock::now() - t0).count();
    emit(reply);
  }
  return 0;
}
