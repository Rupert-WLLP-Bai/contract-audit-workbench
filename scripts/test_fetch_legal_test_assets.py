import hashlib
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest

import fetch_legal_test_assets as fetcher


class LegalTestAssetFetchTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.payload = b"official bytes\x00kept exactly"
        self.config = {
            "schema_version": "legal-test-assets/v1",
            "release_url": "https://github.com/example/project/releases/download/tag/assets.tar.gz",
            "archive_sha256": "",
            "archive_size_bytes": 0,
            "files": [
                {
                    "path": "legal-sources/raw/sample.html",
                    "size_bytes": len(self.payload),
                    "sha256": hashlib.sha256(self.payload).hexdigest(),
                }
            ],
        }
        self.archive = self.make_archive()
        self.config["archive_sha256"] = hashlib.sha256(self.archive).hexdigest()
        self.config["archive_size_bytes"] = len(self.archive)

    def tearDown(self):
        self.temp.cleanup()

    def make_archive(self, members=None):
        members = members or [("legal-sources/raw/sample.html", self.payload)]
        output = io.BytesIO()
        with tarfile.open(fileobj=output, mode="w:gz", format=tarfile.USTAR_FORMAT) as bundle:
            for name in ("legal-sources", "legal-sources/raw"):
                info = tarfile.TarInfo(name + "/")
                info.type = tarfile.DIRTYPE
                info.mode = 0o755
                info.uid = info.gid = 0
                info.uname = info.gname = ""
                info.mtime = 0
                bundle.addfile(info)
            for name, data in members:
                info = tarfile.TarInfo(name)
                info.size = len(data)
                info.mode = 0o644
                info.uid = info.gid = 0
                info.uname = info.gname = ""
                info.mtime = 0
                bundle.addfile(info, io.BytesIO(data))
        return output.getvalue()

    def test_download_extracts_exact_original_bytes(self):
        destination = self.root / "assets"
        fetcher.prepare_assets(destination, self.config, lambda url: self.archive)
        self.assertEqual((destination / "legal-sources/raw/sample.html").read_bytes(), self.payload)

    def test_existing_valid_directory_is_reused_without_download(self):
        destination = self.root / "assets"
        path = destination / "legal-sources/raw/sample.html"
        path.parent.mkdir(parents=True)
        path.write_bytes(self.payload)
        fetcher.prepare_assets(destination, self.config, lambda url: self.fail("unexpected download"))
        self.assertEqual(path.read_bytes(), self.payload)

    def test_corrupt_archive_digest_is_rejected(self):
        config = dict(self.config, archive_sha256="0" * 64)
        with self.assertRaises(fetcher.AssetError):
            fetcher.prepare_assets(self.root / "assets", config, lambda url: self.archive)

    def test_member_bytes_must_match_pinned_file_digest(self):
        altered = self.payload[:-1] + b"X"
        archive = self.make_archive([("legal-sources/raw/sample.html", altered)])
        config = dict(self.config,
                      archive_sha256=hashlib.sha256(archive).hexdigest(),
                      archive_size_bytes=len(archive))
        with self.assertRaises(fetcher.AssetError):
            fetcher.prepare_assets(self.root / "assets", config, lambda url: archive)

    def test_path_traversal_member_is_rejected_before_writing(self):
        malicious = self.make_archive([("../escape", b"no")])
        config = dict(self.config,
                      archive_sha256=hashlib.sha256(malicious).hexdigest(),
                      archive_size_bytes=len(malicious))
        destination = self.root / "assets"
        with self.assertRaises(fetcher.AssetError):
            fetcher.prepare_assets(destination, config, lambda url: malicious)
        self.assertFalse((self.root / "escape").exists())
        self.assertFalse(destination.exists())

    def test_symlink_member_is_rejected(self):
        output = io.BytesIO()
        with tarfile.open(fileobj=output, mode="w:gz", format=tarfile.USTAR_FORMAT) as bundle:
            for name in ("legal-sources", "legal-sources/raw"):
                info = tarfile.TarInfo(name + "/")
                info.type = tarfile.DIRTYPE
                info.mode = 0o755
                bundle.addfile(info)
            link = tarfile.TarInfo("legal-sources/raw/sample.html")
            link.type = tarfile.SYMTYPE
            link.linkname = "../../outside"
            bundle.addfile(link)
        archive = output.getvalue()
        config = dict(self.config,
                      archive_sha256=hashlib.sha256(archive).hexdigest(),
                      archive_size_bytes=len(archive))
        with self.assertRaises(fetcher.AssetError):
            fetcher.prepare_assets(self.root / "assets", config, lambda url: archive)
        self.assertFalse((self.root / "outside").exists())

    def test_modified_existing_directory_is_rejected_not_overwritten(self):
        destination = self.root / "assets"
        path = destination / "legal-sources/raw/sample.html"
        path.parent.mkdir(parents=True)
        path.write_bytes(b"modified")
        with self.assertRaises(fetcher.AssetError):
            fetcher.prepare_assets(destination, self.config, lambda url: self.archive)
        self.assertEqual(path.read_bytes(), b"modified")


if __name__ == "__main__":
    unittest.main()
