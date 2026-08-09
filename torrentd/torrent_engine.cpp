/**
 * NimOS Torrent Engine — Implementation
 */

#include "torrent_engine.h"
#include <libtorrent/settings_pack.hpp>
#include <libtorrent/magnet_uri.hpp>
#include <libtorrent/torrent_info.hpp>
#include <libtorrent/bencode.hpp>
#include <libtorrent/write_resume_data.hpp>
#include <libtorrent/read_resume_data.hpp>
#include <libtorrent/alert_types.hpp>
#include <fstream>
#include <sstream>
#include <filesystem>
#include <iostream>
#include <stdexcept>
#include <sys/statvfs.h>
#include <chrono>

namespace fs = std::filesystem;

namespace {
std::string poolMountForPath(const std::string& path) {
    const std::string prefix = "/nimos/pools/";
    if (path.rfind(prefix, 0) != 0) return "";
    auto end = path.find('/', prefix.size());
    return end == std::string::npos ? path : path.substr(0, end);
}

bool isMountedWritablePoolPath(const std::string& path) {
    const auto pool_mount = poolMountForPath(path);
    if (pool_mount.empty()) return false;

    std::ifstream mounts("/proc/self/mountinfo");
    std::string line;
    bool mounted = false;
    while (std::getline(mounts, line)) {
        std::istringstream fields(line);
        std::string field;
        int index = 0;
        while (fields >> field) {
            if (index++ == 4 && field == pool_mount) {
                mounted = true;
                break;
            }
        }
        if (mounted) break;
    }
    if (!mounted) return false;

    struct statvfs fs_info {};
    return statvfs(pool_mount.c_str(), &fs_info) == 0 &&
           (fs_info.f_flag & ST_RDONLY) == 0;
}
}

// ═══════════════════════════════════
// Constructor / Destructor
// ═══════════════════════════════════

TorrentEngine::TorrentEngine(const std::string& config_path, const std::string& state_path)
    : config_path_(config_path), state_path_(state_path)
{
    // Read default save path from config or use fallback
    default_save_path_ = "";

    // Try to read config
    std::ifstream conf(config_path_);
    if (conf.is_open()) {
        std::string line;
        while (std::getline(conf, line)) {
            if (line.find("download_dir=") == 0) {
                default_save_path_ = line.substr(13);
            }
        }
    }


    // Sin un mount real y escribible no existe ningún fallback. Dejar la ruta
    // vacía es intencional: las altas y restauraciones se rechazan/omiten.
    if (!default_save_path_.empty() && !isMountedWritablePoolPath(default_save_path_)) {
        std::cerr << "[torrentd] WARNING: unsafe download_dir '" << default_save_path_
                  << "' — downloads disabled\n";
        default_save_path_ = "";
    }

    // Only create directories if the path exists and is on a real pool
    if (!default_save_path_.empty()) {
        try {
            if (fs::exists(default_save_path_.substr(0, default_save_path_.rfind('/')))) {
                fs::create_directories(default_save_path_);
            }
        } catch (std::exception& e) {
            std::cerr << "[torrentd] WARNING: cannot create download dir: " << e.what() << "\n";
        }
    }

    try {
        fs::create_directories(state_path_);
    } catch (std::exception& e) {
        std::cerr << "[torrentd] ERROR: cannot create state dir: " << e.what() << "\n";
    }

    // Configure libtorrent session
    lt::settings_pack settings;

    settings.set_int(lt::settings_pack::alert_mask,
        lt::alert_category::status |
        lt::alert_category::error |
        lt::alert_category::storage |
        lt::alert_category::tracker);

    // Performance tuning for NAS (HDD-friendly)
    settings.set_int(lt::settings_pack::active_downloads, 5);
    settings.set_int(lt::settings_pack::active_seeds, 8);
    settings.set_int(lt::settings_pack::active_limit, 15);
    settings.set_int(lt::settings_pack::connections_limit, 200);

    // Disk IO tuning
    settings.set_int(lt::settings_pack::aio_threads, 4);

    // Enable DHT, PEX, LSD for decentralized discovery
    settings.set_bool(lt::settings_pack::enable_dht, true);
    settings.set_bool(lt::settings_pack::enable_lsd, true);

    session_ = std::make_unique<lt::session>(settings);

    // Load saved torrents
    loadState();
    safety_watchdog_ = std::thread(&TorrentEngine::storageSafetyLoop, this);
}

TorrentEngine::~TorrentEngine() {
    stop_safety_watchdog_ = true;
    if (safety_watchdog_.joinable()) safety_watchdog_.join();
    saveState();
}

// ═══════════════════════════════════
// Torrent Operations
// ═══════════════════════════════════

std::string TorrentEngine::addMagnet(const std::string& magnet, const std::string& save_path) {
    std::lock_guard<std::mutex> lock(mutex_);

    lt::add_torrent_params p = lt::parse_magnet_uri(magnet);
    p.save_path = requireSafeSavePath(save_path);

    // CRITICAL: Set flags BEFORE add_torrent — not running, not auto_managed
    p.flags &= ~lt::torrent_flags::paused;
    p.flags &= ~lt::torrent_flags::auto_managed;

    lt::torrent_handle h = session_->add_torrent(p);
    std::string hash = hashToHex(h);

    // Save magnet for persistence
    std::ofstream f(state_path_ + "/" + hash + ".magnet");
    f << magnet << "\n" << p.save_path;
    f.close();

    return hash;
}

std::string TorrentEngine::addTorrentFile(const std::string& torrent_path, const std::string& save_path) {
    std::lock_guard<std::mutex> lock(mutex_);

    lt::add_torrent_params p;
    p.ti = std::make_shared<lt::torrent_info>(torrent_path);
    p.save_path = requireSafeSavePath(save_path);

    // CRITICAL: Set flags BEFORE add_torrent
    p.flags &= ~lt::torrent_flags::paused;
    p.flags &= ~lt::torrent_flags::auto_managed;

    lt::torrent_handle h = session_->add_torrent(p);
    return hashToHex(h);
}

bool TorrentEngine::pause(const std::string& hash) {
    std::lock_guard<std::mutex> lock(mutex_);
    auto h = findHandle(hash);
    if (!h) return false;
    h->unset_flags(lt::torrent_flags::auto_managed);
    h->pause();
    return true;
}

bool TorrentEngine::resume(const std::string& hash) {
    std::lock_guard<std::mutex> lock(mutex_);
    auto h = findHandle(hash);
    if (!h) return false;
    if (!isMountedWritablePoolPath(h->status().save_path)) return false;
    h->unset_flags(lt::torrent_flags::auto_managed);
    h->unset_flags(lt::torrent_flags::paused);
    h->resume();
    return true;
}

bool TorrentEngine::remove(const std::string& hash, bool delete_files) {
    std::lock_guard<std::mutex> lock(mutex_);
    auto h = findHandle(hash);
    if (!h) return false;

    if (delete_files) {
        session_->remove_torrent(*h, lt::session::delete_files);
    } else {
        session_->remove_torrent(*h);
    }

    // Remove persisted state
    try {
        fs::remove(state_path_ + "/" + hash + ".magnet");
        fs::remove(state_path_ + "/" + hash + ".resume");
    } catch (...) {}

    return true;
}

// ═══════════════════════════════════
// Queries
// ═══════════════════════════════════

std::vector<TorrentInfo> TorrentEngine::list() {
    std::lock_guard<std::mutex> lock(mutex_);
    std::vector<TorrentInfo> result;

    for (auto const& h : session_->get_torrents()) {
        auto st = h.status();
        TorrentInfo info;
        info.hash = hashToHex(h);
        info.name = st.name.empty() ? "(loading metadata...)" : st.name;
        info.save_path = st.save_path;
        info.progress = st.progress;
        info.download_rate = st.download_rate;
        info.upload_rate = st.upload_rate;
        info.total_done = st.total_done;
        info.total_wanted = st.total_wanted;
        info.num_peers = st.num_peers;
        info.num_seeds = st.num_seeds;
        info.paused = (st.flags & lt::torrent_flags::paused) != lt::torrent_flags_t{};
        info.state = stateToString(st.state, info.paused);
        result.push_back(info);
    }

    return result;
}

// ═══════════════════════════════════
// Settings
// ═══════════════════════════════════

void TorrentEngine::setDownloadLimit(int bytes_per_sec) {
    lt::settings_pack p;
    p.set_int(lt::settings_pack::download_rate_limit, bytes_per_sec);
    session_->apply_settings(p);
}

void TorrentEngine::setUploadLimit(int bytes_per_sec) {
    lt::settings_pack p;
    p.set_int(lt::settings_pack::upload_rate_limit, bytes_per_sec);
    session_->apply_settings(p);
}

void TorrentEngine::setMaxActive(int max) {
    lt::settings_pack p;
    p.set_int(lt::settings_pack::active_limit, max);
    session_->apply_settings(p);
}

// ═══════════════════════════════════
// Persistence
// ═══════════════════════════════════

void TorrentEngine::saveState() {
    std::lock_guard<std::mutex> lock(mutex_);

    for (auto const& h : session_->get_torrents()) {
        h.save_resume_data(lt::torrent_handle::save_info_dict);
    }

    // Process alerts to get resume data
    std::vector<lt::alert*> alerts;
    session_->pop_alerts(&alerts);

    for (auto* a : alerts) {
        if (auto* rd = lt::alert_cast<lt::save_resume_data_alert>(a)) {
            std::string hash = hashToHex(rd->handle);
            std::vector<char> buf = lt::write_resume_data_buf(rd->params);

            std::ofstream f(state_path_ + "/" + hash + ".resume", std::ios::binary);
            f.write(buf.data(), buf.size());
        }
    }
}

void TorrentEngine::loadState() {
    if (!fs::exists(state_path_)) return;

    for (auto& entry : fs::directory_iterator(state_path_)) {
        std::string file = entry.path().filename().string();

        // Load from resume data
        if (file.ends_with(".resume")) {
            try {
                std::ifstream f(entry.path(), std::ios::binary);
                std::vector<char> buf((std::istreambuf_iterator<char>(f)), std::istreambuf_iterator<char>());

                lt::add_torrent_params p = lt::read_resume_data(buf);

                // Nunca restaurar sobre una carpeta que solo parece un pool.
                // Si el mount desapareció, esa ruta cae en el disco del sistema.
                if (!isMountedWritablePoolPath(p.save_path)) {
                    std::cerr << "[torrentd] Skipping resume " << file
                              << " — destination pool is not mounted and writable\n";
                    continue;
                }

                // Force running state on load
                p.flags &= ~lt::torrent_flags::paused;
                p.flags &= ~lt::torrent_flags::auto_managed;
                session_->async_add_torrent(p);
            } catch (std::exception& e) {
                std::cerr << "[torrentd] Failed to load resume: " << file << " — " << e.what() << "\n";
            }
        }
        // Fallback: load from saved magnets (no resume data yet)
        else if (file.ends_with(".magnet")) {
            std::string hash = file.substr(0, file.size() - 7);
            // Only load if no .resume exists
            if (fs::exists(state_path_ + "/" + hash + ".resume")) continue;

            try {
                std::ifstream f(entry.path());
                std::string magnet, save_path;
                std::getline(f, magnet);
                std::getline(f, save_path);

                lt::add_torrent_params p = lt::parse_magnet_uri(magnet);

                if (isMountedWritablePoolPath(save_path)) {
                    p.save_path = save_path;
                } else {
                    std::cerr << "[torrentd] Skipping magnet " << hash 
                              << " — no valid pool path available\n";
                    continue;
                }

                // Force running state on load
                p.flags &= ~lt::torrent_flags::paused;
                p.flags &= ~lt::torrent_flags::auto_managed;
                session_->async_add_torrent(p);
            } catch (std::exception& e) {
                std::cerr << "[torrentd] Failed to load magnet: " << file << " — " << e.what() << "\n";
            }
        }
    }
}

std::string TorrentEngine::requireSafeSavePath(const std::string& requested_path) const {
    const auto& resolved = requested_path.empty() ? default_save_path_ : requested_path;
    if (!isMountedWritablePoolPath(resolved)) {
        throw std::invalid_argument("destination must be on a mounted writable NimOS pool");
    }
    return resolved;
}

void TorrentEngine::storageSafetyLoop() {
    while (!stop_safety_watchdog_) {
        for (int i = 0; i < 20 && !stop_safety_watchdog_; ++i) {
            std::this_thread::sleep_for(std::chrono::milliseconds(100));
        }
        if (stop_safety_watchdog_) break;

        std::lock_guard<std::mutex> lock(mutex_);
        for (auto const& handle : session_->get_torrents()) {
            auto status = handle.status();
            if (!isMountedWritablePoolPath(status.save_path) &&
                (status.flags & lt::torrent_flags::paused) == lt::torrent_flags_t{}) {
                std::cerr << "[torrentd] SAFETY: pausing '" << status.name
                          << "' — destination pool is no longer writable\n";
                handle.unset_flags(lt::torrent_flags::auto_managed);
                handle.pause();
            }
        }
    }
}

// ═══════════════════════════════════
// Private helpers
// ═══════════════════════════════════

std::optional<lt::torrent_handle> TorrentEngine::findHandle(const std::string& hash) {
    for (auto& h : session_->get_torrents()) {
        if (hashToHex(h) == hash) {
            return h;
        }
    }
    return std::nullopt;
}

std::string TorrentEngine::hashToHex(const lt::torrent_handle& h) {
    auto st = h.status(lt::torrent_handle::query_name);
    std::stringstream ss;
    ss << st.info_hashes.get_best();
    return ss.str();
}

std::string TorrentEngine::stateToString(lt::torrent_status::state_t s, bool paused) {
    if (paused) return "paused";
    switch (s) {
        case lt::torrent_status::checking_files: return "checking";
        case lt::torrent_status::downloading_metadata: return "metadata";
        case lt::torrent_status::downloading: return "downloading";
        case lt::torrent_status::finished: return "finished";
        case lt::torrent_status::seeding: return "seeding";
        case lt::torrent_status::checking_resume_data: return "checking";
        default: return "unknown";
    }
}
