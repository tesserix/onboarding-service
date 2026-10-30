"""Release contracts that prevent unbuilt commits reaching production."""
import json
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[1]


class DeliveryTests(unittest.TestCase):
    def test_ci_uses_shared_quality_and_image_gates(self):
        workflow = (ROOT / '.github/workflows/ci.yml').read_text()
        for name in ('go-ci', 'secret-scan', 'container-ci', 'container-release'):
            self.assertRegex(workflow, rf'tesserix/tesserix-workflows/.github/workflows/{name}\.yml@[0-9a-f]{{40}}')
        self.assertIn('database_enabled: true', workflow)
        self.assertNotIn('working_directory: apps/web', workflow)
        self.assertIn('needs: [contracts, api, secrets, containers]', workflow)
        self.assertIn('needs: [release]', workflow)
        self.assertIn('"${GITHUB_SHA}:refs/heads/deploy"', workflow)
        self.assertNotIn('--force', workflow)
        self.assertNotIn('continue-on-error', workflow)

    def test_api_toolchain_matches_required_go_version(self):
        expected = '1.27.3'
        module = (ROOT / 'apps/api/go.mod').read_text()
        self.assertEqual(re.search(r'^go (\S+)$', module, re.M).group(1), expected)
        self.assertEqual(re.search(r'^go (\S+)$', (ROOT / 'go.work').read_text(), re.M).group(1), expected)
        self.assertIn('go_version: ' + expected, (ROOT / '.github/workflows/ci.yml').read_text())
        self.assertIn('ENV GOTOOLCHAIN=go' + expected, (ROOT / 'deploy/Dockerfile.api').read_text())

    def test_image_contract_covers_only_the_retained_private_api(self):
        images = json.loads((ROOT / '.github/container-images.json').read_text())
        self.assertEqual({image['name'] for image in images}, {'onboarding-api'})
        for image in images:
            self.assertEqual(image['context'], '.')
            self.assertEqual(image['target'], 'runtime')
            self.assertTrue((ROOT / image['dockerfile']).is_file())
        self.assertFalse((ROOT / 'apps/web/package.json').exists())
        self.assertFalse((ROOT / 'deploy/Dockerfile.web').exists())

    def test_images_use_only_digest_pinned_shared_bases(self):
        for name in ('api',):
            dockerfile = (ROOT / f'deploy/Dockerfile.{name}').read_text()
            bases = re.findall(r'^FROM (?:--platform=\S+ )?(\S+)', dockerfile, re.M)
            self.assertEqual(len(bases), 2)
            for base in bases:
                self.assertRegex(base, r'^ghcr.io/tesserix/base-[\w-]+:latest@sha256:[a-f0-9]{64}$')
            self.assertIn('USER 1000:1000', dockerfile)
        ignore = (ROOT / '.dockerignore').read_text()
        for pattern in ('**/node_modules', '**/.next', '**/.env', '.git'):
            self.assertIn(pattern, ignore)


if __name__ == '__main__':
    unittest.main()
