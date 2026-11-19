#include <dlfcn.h>
#include <stdbool.h>
#include <stdio.h>

int main(void) {
    void *lib = dlopen("/usr/lib/libSNPE.so", RTLD_NOW | RTLD_LOCAL);
    if (!lib) {
        fprintf(stderr, "%s\n", dlerror());
        return 1;
    }
    bool (*available)(int) = dlsym(lib, "_ZN3zdl4SNPE11SNPEFactory18isRuntimeAvailableENS_8DlSystem9Runtime_tE");
    if (!available) {
        fprintf(stderr, "%s\n", dlerror());
        return 2;
    }
    const char *runtime[] = {"cpu", "gpu", "hexagon", "gpu_fp16"};
    for (int i = 0; i < 4; ++i) printf("%s=%d\n", runtime[i], available(i));
    dlclose(lib);
    return 0;
}
