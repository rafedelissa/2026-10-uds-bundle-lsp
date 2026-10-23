import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createVSIX } from '@vscode/vsce';
import { buildLsp, lspTargets, selectLspTargets } from '../../hack/build-lsp.mjs';

const extensionRoot = fileURLToPath(new URL('../', import.meta.url));
const repositoryRoot = fileURLToPath(new URL('../../', import.meta.url));

async function main() {
    const selectedTargets = selectLspTargets(process.argv.slice(2), process.env.UDS_VSCODE_TARGET);

    const manifest = JSON.parse(readFileSync(join(extensionRoot, 'package.json'), 'utf8'));
    // The client is already bundled by prepackage:vsix. Staging contains only runtime files.
    delete manifest.scripts;
    delete manifest.devDependencies;
    manifest.files = ['bin/**', 'dist/**', 'README.md', 'CHANGELOG.md', 'LICENSE'];
    mkdirSync(join(repositoryRoot, 'build'), { recursive: true });

    for (const target of selectedTargets) {
        const { goos } = lspTargets[target];
        const builtServer = buildLsp(target);
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
            copyFileSync(builtServer, executable);

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
