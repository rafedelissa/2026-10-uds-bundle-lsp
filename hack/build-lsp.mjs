import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { chmodSync, existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join, relative, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

export const lspTargets = {
    'darwin-arm64': { goos: 'darwin', goarch: 'arm64' },
    'darwin-x64': { goos: 'darwin', goarch: 'amd64' },
    'linux-arm64': { goos: 'linux', goarch: 'arm64' },
    'linux-x64': { goos: 'linux', goarch: 'amd64' },
    'win32-arm64': { goos: 'windows', goarch: 'arm64' },
    'win32-x64': { goos: 'windows', goarch: 'amd64' }
};

const repositoryRoot = fileURLToPath(new URL('../', import.meta.url));

export function selectLspTargets(args, defaultTarget) {
    let selected;
    if (args.length === 1 && args[0] === '--all') {
        selected = Object.keys(lspTargets);
    } else if (args.length === 2 && args[0] === '--target') {
        selected = [args[1]];
    } else if (args.length === 0) {
        selected = [defaultTarget || `${process.platform}-${process.arch}`];
    } else {
        throw new Error('Expected --target <target> or --all.');
    }
    for (const target of selected) {
        if (!Object.hasOwn(lspTargets, target)) {
            throw new Error(`Unsupported LSP target ${JSON.stringify(target)}. Choose ${Object.keys(lspTargets).join(', ')}.`);
        }
    }
    return selected;
}

function artifactPath({ goos, goarch }) {
    return join(repositoryRoot, 'build', `uds-lsp-${goos}-${goarch}${goos === 'windows' ? '.exe' : ''}`);
}

export function buildLsp(target) {
    selectLspTargets(['--target', target]);
    const { goos, goarch } = lspTargets[target];
    const executable = artifactPath(lspTargets[target]);
    mkdirSync(join(repositoryRoot, 'build'), { recursive: true });
    console.log(`Building LSP for ${target} (GOOS=${goos}, GOARCH=${goarch})...`);
    execFileSync('go', ['build', '-mod=mod', '-trimpath', '-ldflags=-s -w', '-o', executable, './cmd/uds-lsp'], {
        cwd: repositoryRoot,
        env: { ...process.env, CGO_ENABLED: '0', GOOS: goos, GOARCH: goarch },
        stdio: 'inherit'
    });
    chmodSync(executable, 0o755);

    // Cover every available release variant, including variants from earlier invocations.
    const checksums = Object.values(lspTargets).map(artifactPath).filter(existsSync).map(file => {
        const hash = createHash('sha256').update(readFileSync(file)).digest('hex');
        return `${hash}  ${relative(repositoryRoot, file).split(sep).join('/')}`;
    });
    writeFileSync(join(repositoryRoot, 'build', 'lsp-checksums.txt'), checksums.join('\n') + '\n');
    return executable;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
    try {
        for (const target of selectLspTargets(process.argv.slice(2), process.env.UDS_LSP_TARGET)) {
            console.log(`Built ${buildLsp(target)}`);
        }
    } catch (error) {
        console.error(error.message);
        process.exitCode = 1;
    }
}
