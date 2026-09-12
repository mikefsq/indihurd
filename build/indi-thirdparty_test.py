"""Verify selected installation preserves symlinks and excludes unrelated builds."""
import importlib.machinery
import importlib.util
from pathlib import Path
import tempfile
import unittest

loader = importlib.machinery.SourceFileLoader('thirdparty', str(Path(__file__).with_name('indi-thirdparty')))
spec = importlib.util.spec_from_loader(loader.name, loader)
build = importlib.util.module_from_spec(spec)
loader.exec_module(build)


class InstallTests(unittest.TestCase):
    def test_selected_manifests_and_symlinks(self):
        with tempfile.TemporaryDirectory() as tmp:
            work = Path(tmp)
            prefix = work / 'prefix'
            (prefix / 'lib').mkdir(parents=True)
            binary = prefix / 'lib/libvendor.so.1'
            binary.write_bytes(b'vendor SDK')
            link = prefix / 'lib/libvendor.so'
            link.symlink_to(binary.name)
            (prefix / 'lib/unrelated.so').write_bytes(b'old thirdparty build')
            for project in build.PROJECTS:
                manifest = work / 'selected-thirdparty' / project / 'install_manifest.txt'
                manifest.parent.mkdir(parents=True)
                manifest.write_text(f'{binary}\n{link}\n')
            destination = work / 'installed'
            build.install(work, destination)
            build.install(work, destination)
            self.assertEqual((destination / 'lib/libvendor.so').read_bytes(), b'vendor SDK')
            self.assertTrue((destination / 'lib/libvendor.so').is_symlink())
            self.assertFalse((destination / 'lib/unrelated.so').exists())
            manifest.write_text('/etc/passwd\n')
            with self.assertRaises(ValueError):
                build.install(work, destination)

    def test_qhy_firmware_rule_relocated(self):
        with tempfile.TemporaryDirectory() as tmp:
            work = Path(tmp)
            prefix = work / 'prefix'
            rule = prefix / 'lib/udev/rules.d/85-qhyccd.rules'
            firmware = prefix / 'lib/firmware/qhy/POLEMASTER.HEX'
            rule.parent.mkdir(parents=True)
            firmware.parent.mkdir(parents=True)
            firmware.write_text('firmware fixture')
            text = f'RUN+="/sbin/fxload -I {firmware}"\n'
            rule.write_text(text)
            for project in build.PROJECTS:
                manifest = work / 'selected-thirdparty' / project / 'install_manifest.txt'
                manifest.parent.mkdir(parents=True)
                manifest.write_text(f'{rule}\n{firmware}\n')
            destination = work / 'installed'
            build.install(work, destination)
            installed = destination / rule.relative_to(prefix)
            self.assertIn(str(destination / firmware.relative_to(prefix)), installed.read_text())
            self.assertNotIn(str(prefix), installed.read_text())
            self.assertEqual(rule.read_text(), text)
            self.assertEqual((destination / firmware.relative_to(prefix)).read_text(), 'firmware fixture')

    def test_incomplete_build_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            with self.assertRaisesRegex(RuntimeError, 'Build first'):
                build.install(Path(tmp), Path(tmp) / 'dest')


if __name__ == '__main__':
    unittest.main()
