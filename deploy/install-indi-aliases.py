#!/usr/bin/env python3
"""Install staged driver aliases whose target belongs to the core install manifest."""
import os
import shutil
from pathlib import Path
import sys


def install_aliases(work, prefix):
    staged = work / 'prefix/bin'
    destination = prefix / 'bin'
    manifest = work / 'core/install_manifest.txt'
    if not staged.is_dir() or not manifest.is_file():
        raise RuntimeError('Build and install INDI core before installing its aliases')
    core_targets = {Path(line).name for line in manifest.read_text().splitlines()
                    if Path(line).parent.name == 'bin'}
    aliases = []
    for source in sorted(staged.iterdir()):
        if not source.is_symlink():
            continue
        target = os.readlink(source)
        # Keep aliases relative and restrict them to core-installed executables.
        if Path(target).name != target or target not in core_targets:
            continue
        if not (destination / target).is_file():
            raise RuntimeError(f'Alias {source.name}: installed target {target} is missing')
        aliases.append((source.name, target))
    for name, target in aliases:
        link = destination / name
        if link.is_symlink() and os.readlink(link) == target:
            continue
        temporary = destination / f'.{name}.{os.getpid()}.tmp'
        try:
            temporary.symlink_to(target)
            os.replace(temporary, link)
        finally:
            if temporary.is_symlink():
                temporary.unlink()
    print(f'Installed {len(aliases)} INDI core aliases in {destination}')
    return len(aliases)


def install_catalog(work, prefix):
    # Upstream bakes DATA_INSTALL_DIR into cmake_install.cmake, so --prefix
    # alone leaves this catalog in the staging tree instead of /usr/local.
    source = work / 'prefix/share/indi/drivers.xml'
    if not source.is_file():
        raise RuntimeError('Core driver catalog missing; run make indi-drivers')
    target = prefix / 'share/indi/drivers.xml'
    target.parent.mkdir(parents=True, exist_ok=True)
    temporary = target.with_name('.drivers.xml.indihurd-tmp')
    try:
        shutil.copy2(source, temporary)
        os.replace(temporary, target)
    finally:
        if temporary.exists():
            temporary.unlink()
    print(f'Installed INDI core catalog in {target}')


if __name__ == '__main__':
    try:
        install_aliases(Path(sys.argv[1]).resolve(), Path('/usr/local'))
        install_catalog(Path(sys.argv[1]).resolve(), Path('/usr/local'))
    except (OSError, RuntimeError) as error:
        print(f'Alias installation failed: {error}', file=sys.stderr)
        sys.exit(1)
