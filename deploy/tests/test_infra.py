"""Exercise safe local bootstrap and configuration rejection before Docker runs."""
import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from urllib.parse import urlsplit

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("infra", ROOT / "deploy/scripts/infra.py")
infra = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(infra)


class LocalInfraTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        (self.root / ".env.example").write_bytes((ROOT / ".env.example").read_bytes())
        infra.bootstrap(self.root)
        self.values = infra.read_env(self.root / ".env")

    def test_generates_distinct_secrets_and_consistent_urls(self):
        infra.validate_env(self.values)
        self.assertEqual(len({self.values[k] for k in infra.SECRET_KEYS}), len(infra.SECRET_KEYS))
        self.assertIn(self.values['POSTGRES_PASSWORD'], self.values['DEV_DATABASE_URL'])
        self.assertEqual((self.root / '.env').stat().st_mode & 0o777, 0o600)

    def test_custom_profile_uses_private_unique_resources(self):
        profile = self.root / "ev02-profile" / ".env"
        original = (self.root / ".env").read_bytes()
        with patch.dict(infra.os.environ, {"INFRA_ENV_PATH": str(profile), "COMPOSE_PROJECT_NAME": ""}):
            message = infra.bootstrap(self.root)
            values = infra.read_env(profile)
            project = infra.isolated_project_name(profile, self.root)
        self.assertIn("isolated profile", message)
        self.assertEqual(profile.stat().st_mode & 0o777, 0o600)
        self.assertEqual((self.root / ".env").read_bytes(), original)
        self.assertRegex(urlsplit(values["DEV_DATABASE_URL"]).path, r"^/contract_audit_dev_[a-f0-9]{8}$")
        self.assertRegex(urlsplit(values["TEST_DATABASE_URL"]).path, r"^/contract_audit_test_[a-f0-9]{8}$")
        port_keys = ("POSTGRES_PORT", "RUSTFS_PORT", "RUSTFS_CONSOLE_PORT", "KEYCLOAK_PORT",
                     "EDGE_PORT", "CLIENT_PORT", "ADMIN_PORT", "PORTAL_PORT")
        self.assertEqual(len({values[key] for key in port_keys}), len(port_keys))
        self.assertEqual(values["S3_ENDPOINT"], f"http://127.0.0.1:{values['RUSTFS_PORT']}")
        self.assertRegex(values["S3_BUCKET"], r"^contract-audit-[a-f0-9]{8}$")
        self.assertTrue(project.startswith("contract-audit-ev02-profile-"))
    def test_existing_config_is_preserved(self):
        before = (self.root / '.env').read_bytes()
        self.assertIn('reused', infra.bootstrap(self.root))
        self.assertEqual((self.root / '.env').read_bytes(), before)

    def test_unsafe_existing_config_is_rejected_without_overwrite(self):
        (self.root / '.env').write_text('POSTGRES_PASSWORD=contractaudit\n')
        with self.assertRaisesRegex(ValueError, 'POSTGRES_PASSWORD'):
            infra.bootstrap(self.root)
        self.assertEqual((self.root / '.env').read_text(), 'POSTGRES_PASSWORD=contractaudit\n')

    def test_port_mismatch_and_collision_are_rejected(self):
        self.values['POSTGRES_PORT'] = '6543'
        with self.assertRaisesRegex(ValueError, 'port must match'):
            infra.validate_env(self.values)
        self.values['POSTGRES_PORT'] = self.values['RUSTFS_PORT']
        with self.assertRaisesRegex(ValueError, 'distinct'):
            infra.validate_env(self.values)
        self.values['POSTGRES_PORT'] = '0' + self.values['RUSTFS_PORT']
        with self.assertRaisesRegex(ValueError, 'distinct'):
            infra.validate_env(self.values)

    def test_foreign_database_and_nonlocal_url_are_rejected(self):
        self.values['TEST_DATABASE_URL'] = self.values['DEV_DATABASE_URL']
        with self.assertRaisesRegex(ValueError, 'separate'):
            infra.validate_env(self.values)
        self.values['DEV_DATABASE_URL'] = self.values['DEV_DATABASE_URL'].replace('127.0.0.1', 'prod.example')
        with self.assertRaisesRegex(ValueError, 'loopback'):
            infra.validate_env(self.values)

    def test_unpinned_images_and_bad_bucket_are_rejected(self):
        for image in ('rustfs/rustfs:latest', 'rustfs/rustfs'):
            self.values['RUSTFS_IMAGE'] = image
            with self.assertRaisesRegex(ValueError, 'Pin an explicit'):
                infra.validate_env(self.values)
        self.values['RUSTFS_IMAGE'] = 'rustfs/rustfs:1.0.3'
        self.values['S3_BUCKET'] = '../foreign-data'
        with self.assertRaisesRegex(ValueError, 'S3_BUCKET'):
            infra.validate_env(self.values)

    def test_dotenv_cannot_execute_shell_or_duplicate_keys(self):
        for content in ('X=$(cat /secret)\n', 'X=ok\nX=bad\n', 'export X=foo\n'):
            (self.root / '.env').write_text(content)
            with self.assertRaises(ValueError):
                infra.read_env(self.root / '.env')


if __name__ == '__main__':
    unittest.main()
