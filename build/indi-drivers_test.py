"""Exercise orchestration without fetching or installing upstream software."""
import importlib.machinery
import importlib.util
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

loader = importlib.machinery.SourceFileLoader('indi_build', str(Path(__file__).with_name('indi-drivers')))
spec = importlib.util.spec_from_loader(loader.name, loader)
build = importlib.util.module_from_spec(spec)
loader.exec_module(build)


class BuildTests(unittest.TestCase):
    def test_core_only_build(self):
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            calls = []
            def run(*args, **kwargs):
                calls.append(tuple(str(a) for a in args))
                return '' if 'status' in args else 'abc123'
            with patch.dict(os.environ, {'INDI_BUILD_DIR':directory, 'INDI_JOBS':'3',
                      'INDI_REF':'core-tag', 'INDI_CMAKE_ARGS':'-DFIX_WARNINGS=ON'}, clear=True), patch.object(build, 'run', run):
                build.main()
            self.assertFalse(any('indi-3rdparty' in arg or 'libbno08x' in arg for c in calls for arg in c))
            self.assertFalse(any(c[0] in ('sudo', 'ldconfig') for c in calls))
            configs = [c for c in calls if '-S' in c]
            self.assertEqual(len(configs), 1)
            self.assertIn(str(work/'src/indi'), configs[0])
            self.assertIn(f'-DCMAKE_INSTALL_PREFIX={work / "prefix"}', configs[0])
            self.assertIn('-DCMAKE_INSTALL_RPATH=$ORIGIN/../lib;$ORIGIN', configs[0])
            self.assertGreater(configs[0].index('-DFIX_WARNINGS=ON'), configs[0].index('-DFIX_WARNINGS=OFF'))
            installs = [c for c in calls if '--install' in c]
            self.assertEqual(installs, [('cmake', '--install', str(work/'core'), '--prefix', str(work/'prefix'))])
            self.assertEqual((work/'sources.txt').read_text(), 'indi abc123\n')

    def test_relocated_cmake_build(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            original = root / 'old'
            source = original / 'src'
            target = original / 'core'
            source.mkdir(parents=True)
            (source / 'CMakeLists.txt').write_text('cmake_minimum_required(VERSION 3.16)\nproject(relocation NONE)\n')
            subprocess.run(['cmake', '-S', str(source), '-B', str(target)], check=True,
                           stdout=subprocess.DEVNULL)
            # Matching caches keep incremental build output.
            marker = target / 'keep'
            marker.write_text('generated')
            build.reset_relocated_build(target, source)
            self.assertTrue(marker.exists())
            moved = root / 'build'
            original.rename(moved)
            build.reset_relocated_build(moved / 'core', moved / 'src')
            self.assertFalse((moved / 'core').exists())
            self.assertTrue((moved / 'src/CMakeLists.txt').exists())
            subprocess.run(['cmake', '-S', str(moved / 'src'), '-B', str(moved / 'core')],
                           check=True, stdout=subprocess.DEVNULL)
            cache = (moved / 'core/CMakeCache.txt').read_text()
            self.assertIn(f'CMAKE_HOME_DIRECTORY:INTERNAL={moved / "src"}', cache)

    def test_dirty_checkout_refused(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(build,'run',return_value=' M edited') as run:
            with self.assertRaisesRegex(RuntimeError,'local changes'):
                build.checkout(Path(directory),'unused','master')
            self.assertEqual(run.call_count,1)


if __name__ == '__main__':
    unittest.main()
