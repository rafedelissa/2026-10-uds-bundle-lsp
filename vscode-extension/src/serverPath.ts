import { resolve } from 'node:path';

interface ServerPathOptions {
    configuredPath?: string;
    extensionPath: string;
    development: boolean;
    platform?: NodeJS.Platform;
}

export function resolveServerPath({
    configuredPath,
    extensionPath,
    development,
    platform = process.platform
}: ServerPathOptions): string {
    if (configuredPath) {
        return configuredPath;
    }

    const executable = platform === 'win32' ? 'uds-lsp.exe' : 'uds-lsp';
    return resolve(extensionPath, development ? '../build' : 'bin', executable);
}
