/**
 * NimOS Torrent Engine — libtorrent wrapper
 * Manages torrent sessions, persistence, and state
 */

#pragma once

#include <libtorrent/session.hpp>
#include <libtorrent/torrent_handle.hpp>
#include <libtorrent/torrent_status.hpp>
#include <vector>
#include <string>
#include <mutex>
#include <memory>
#include <optional>

namespace lt = libtorrent;

struct TorrentInfo {
    std::string hash;
    std::string name;
    std::string save_path;
    std::string magnet;
    float progress;
    int64_t download_rate;
    int64_t upload_rate;
    int64_t total_done;
    int64_t total_wanted;
    int num_peers;
    int num_seeds;
    std::string state; // "downloading", "seeding", "paused", "checking", "queued", "error"
    bool paused;
};

class TorrentEngine {
public:
    TorrentEngine(const std::string& config_path, const std::string& state_path);
    ~TorrentEngine();

    // Torrent operations
    std::string addMagnet(const std::string& magnet, const std::string& save_path = "");
    std::string addTorrentFile(const std::string& torrent_path, const std::string& save_path = "");
    bool pause(const std::string& hash);
    bool resume(const std::string& hash);
    bool remove(const std::string& hash, bool delete_files = false);

    // Queries
    std::vector<TorrentInfo> list();
    TorrentInfo* getByHash(const std::string& hash);

    // Settings
    void setDownloadLimit(int bytes_per_sec); // 0 = unlimited
    void setUploadLimit(int bytes_per_sec);
    void setMaxActive(int max);

    // Persistence
    void saveState();
    void loadState();

private:
    std::unique_ptr<lt::session> session_;
    std::string config_path_;
    std::string state_path_;
    std::string default_save_path_;
    std::mutex mutex_;

    std::optional<lt::torrent_handle> findHandle(const std::string& hash);
    std::string hashToHex(const lt::torrent_handle& h);
    std::string stateToString(lt::torrent_status::state_t s, bool paused);
};
