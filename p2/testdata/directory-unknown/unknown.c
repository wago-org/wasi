#define _GNU_SOURCE
#define FUSE_USE_VERSION 31
#include <fuse.h>
#include <dirent.h>
#include <errno.h>
#include <limits.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

static char *backing;
static int hostpath(char *out, const char *path) {
    return snprintf(out, PATH_MAX, "%s%s", backing, path) >= PATH_MAX ? -ENAMETOOLONG : 0;
}
static int getattr_cb(const char *path, struct stat *out, struct fuse_file_info *fi) {
    (void)fi;
    char host[PATH_MAX]; int err = hostpath(host, path);
    if (err) return err;
    return lstat(host, out) ? -errno : 0;
}
static int opendir_cb(const char *path, struct fuse_file_info *fi) {
    char host[PATH_MAX]; int err = hostpath(host, path);
    if (err) return err;
    DIR *dir = opendir(host);
    if (!dir) return -errno;
    fi->fh = (uintptr_t)dir;
    return 0;
}
static int readdir_cb(const char *path, void *buf, fuse_fill_dir_t fill, off_t offset,
                      struct fuse_file_info *fi, enum fuse_readdir_flags flags) {
    (void)path; (void)flags;
    DIR *dir = (DIR *)(uintptr_t)fi->fh;
    seekdir(dir, offset);
    errno = 0;
    struct dirent *entry;
    while ((entry = readdir(dir))) {
        // Supplying no stat leaves each native directory record DT_UNKNOWN.
        if (fill(buf, entry->d_name, NULL, telldir(dir), FUSE_FILL_DIR_DEFAULTS)) break;
    }
    return errno ? -errno : 0;
}
static int releasedir_cb(const char *path, struct fuse_file_info *fi) {
    (void)path;
    return closedir((DIR *)(uintptr_t)fi->fh) ? -errno : 0;
}
static int rename_cb(const char *old, const char *new, unsigned flags) {
    if (flags) return -EINVAL;
    char a[PATH_MAX], b[PATH_MAX]; int err = hostpath(a, old);
    if (!err) err = hostpath(b, new);
    if (err) return err;
    return rename(a, b) ? -errno : 0;
}
static int mkdir_cb(const char *path, mode_t mode) {
    char host[PATH_MAX]; int err = hostpath(host, path);
    if (err) return err;
    return mkdir(host, mode) ? -errno : 0;
}
static int unlink_cb(const char *path) {
    char host[PATH_MAX]; int err = hostpath(host, path);
    if (err) return err;
    return unlink(host) ? -errno : 0;
}
static int rmdir_cb(const char *path) {
    char host[PATH_MAX]; int err = hostpath(host, path);
    if (err) return err;
    return rmdir(host) ? -errno : 0;
}
static int create_cb(const char *path, mode_t mode, struct fuse_file_info *fi) {
    char host[PATH_MAX]; int err = hostpath(host, path);
    if (err) return err;
    int fd = open(host, fi->flags | O_CREAT, mode);
    if (fd < 0) return -errno;
    fi->fh = fd;
    return 0;
}
static int write_cb(const char *path, const char *buf, size_t size, off_t off, struct fuse_file_info *fi) {
    (void)path;
    int got = pwrite(fi->fh, buf, size, off);
    return got < 0 ? -errno : got;
}
static int release_cb(const char *path, struct fuse_file_info *fi) {
    (void)path;
    return close(fi->fh) ? -errno : 0;
}
static const struct fuse_operations ops = {
    .getattr = getattr_cb, .opendir = opendir_cb, .readdir = readdir_cb,
    .releasedir = releasedir_cb, .rename = rename_cb, .mkdir = mkdir_cb,
    .unlink = unlink_cb, .rmdir = rmdir_cb, .create = create_cb,
    .write = write_cb, .release = release_cb,
};
int main(int argc, char **argv) {
    if (argc < 3) return 2;
    backing = realpath(argv[1], NULL);
    if (!backing) return 2;
    argv[1] = argv[0];
    return fuse_main(argc - 1, argv + 1, &ops, NULL);
}
