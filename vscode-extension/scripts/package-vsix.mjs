import { execFileSync } from 'node:child_process';
import { chmodSync, copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createVSIX } from '@vscode/vsce';

const targets = {
    'darwin-arm64': { goos: 'darwin', goarch: 'arm64' },
    'darwin-x64': { goos: 'darwin', goarch: 'amd64' },
    'linux-arm64': { goos: 'linux', goarch: 'arm64' },
    'linux-x64': { goos: 'linux', goarch: 'amd64' },
    'win32-arm64': { goos: 'windows', goarch: 'arm64' },
    'win32-x64': { goos: 'windows', goarch: 'amd64' }
};

const extensionRoot = fileURLToPath(new URL('../', import.meta.url));
const repositoryRoot = fileURLToPath(new URL('../../', import.meta.url));

async function main() {
    const args = process.argv.slice(2);
    let selectedTargets;
    if (args.length === 1 && args[0] === '--all') {
        selectedTargets = Object.keys(targets);
    } else if (args.length === 2 && args[0] === '--target') {
        selectedTargets = [args[1]];
    } else if (args.length === 0) {
        selectedTargets = [process.env.UDS_VSCODE_TARGET || `${process.platform}-${process.arch}`];
    } else {
        throw new Error('Usage: npm run package:vsix -- [--target <target> | --all]');
    }
    for (const target of selectedTargets) {
        if (!Object.hasOwn(targets, target)) {
            throw new Error(`Unsupported VS Code target ${JSON.stringify(target)}. Choose ${Object.keys(targets).join(', ')}.`);
        }
    }

    const manifest = JSON.parse(readFileSync(join(extensionRoot, 'package.json'), 'utf8'));
    // The client is already bundled by prepackage:vsix. Staging contains only runtime files.
    delete manifest.scripts;
    delete manifest.devDependencies;
    manifest.files = ['bin/**', 'dist/**', 'README.md', 'CHANGELOG.md', 'LICENSE'];
    mkdirSync(join(repositoryRoot, 'build'), { recursive: true });

    for (const target of selectedTargets) {
        const { goos, goarch } = targets[target];
        const staging = mkdtempSync(join(tmpdir(), 'uds-vscode-'));
        try {
            mkdirSync(join(staging, 'bin'));
            mkdirSync(join(staging, 'dist'));
            for (const file of ['README.md', 'CHANGELOG.md', 'dist/extension.js']) {
                copyFileSync(join(extensionRoot, file), join(staging, file));
            }
            copyFileSync(join(repositoryRoot, 'LICENSE'), join(staging, 'LICENSE'));
            writeFileSync(join(staging, 'package.json'), JSON.stringify(manifest, null, 2) + '\n');

            const executable = join(staging, 'bin', goos === 'windows' ? 'uds-lsp.exe' : 'uds-lsp');
            console.log(`Building bundled LSP for ${target}...`);
            execFileSync('go', ['build', '-mod=mod', '-trimpath', '-ldflags=-s -w', '-o', executable, './cmd/uds-lsp'], {
                cwd: repositoryRoot,
                env: { ...process.env, CGO_ENABLED: '0', GOOS: goos, GOARCH: goarch },
                stdio: 'inherit'
            });
            chmodSync(executable, 0o755);

            await createVSIX({
                cwd: staging,
                packagePath: join(repositoryRoot, 'build', `uds-lsp-extension-${target}.vsix`),
                target,
                dependencies: false,
                baseContentUrl: 'https://github.com/defenseunicorns/uds-cli/blob/HEAD/vscode-extension/'
            });
        } finally {
            rmSync(staging, { recursive: true, force: true });
        }
    }
}

main().catch(error => {
    console.error(error.message);
    process.exitCode = 1;
});
