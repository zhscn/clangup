#include <format>
#include <stdexcept>
#include <string_view>
#include <thread>

int main() {
    int result = 1;
    std::thread worker([&] {
        try {
            throw std::runtime_error(std::format("{}", 42));
        } catch (const std::runtime_error& error) {
            result = std::string_view(error.what()) != "42";
        }
    });
    worker.join();
    return result;
}
