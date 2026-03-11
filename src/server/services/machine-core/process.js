import fs from 'fs';
import net from 'net';
import path from 'path';
import { spawn } from 'child_process';
import logger from '../../lib/logger';

const log = logger('service:machine-core-process');

const DEFAULT_ADDR = '127.0.0.1:8081';
const START_TIMEOUT_MS = 8000;
const GO_MACHINE_CORE_MODE = /^(\s*)(grpc|goa|go)\s*$/i;

let child = null;
let startupPromise = null;
let shutdownHooksInstalled = false;

const normalizeAddress = (input = DEFAULT_ADDR) => {
    const value = `${input || ''}`.trim();
    if (!value || value === ':') {
        return DEFAULT_ADDR;
    }
    if (value.startsWith(':')) {
        return `127.0.0.1${value}`;
    }
    return value;
};

const parseHostPort = (input = DEFAULT_ADDR) => {
    const normalized = normalizeAddress(input);
    const separatorIndex = normalized.lastIndexOf(':');
    return {
        host: normalized.slice(0, separatorIndex),
        port: Number.parseInt(normalized.slice(separatorIndex + 1), 10),
    };
};

const canConnect = ({ host, port }) => new Promise((resolve) => {
    const socket = net.createConnection({ host, port });
    const finalize = (result) => {
        socket.removeAllListeners();
        socket.destroy();
        resolve(result);
    };

    socket.setTimeout(500);
    socket.once('connect', () => finalize(true));
    socket.once('timeout', () => finalize(false));
    socket.once('error', () => finalize(false));
});

const waitForReady = async (addr, timeoutMs = START_TIMEOUT_MS) => {
    const target = parseHostPort(addr);
    const startedAt = Date.now();
    while ((Date.now() - startedAt) < timeoutMs) {
        // eslint-disable-next-line no-await-in-loop
        if (await canConnect(target)) {
            return true;
        }
        // eslint-disable-next-line no-await-in-loop
        await new Promise((resolve) => setTimeout(resolve, 150));
    }
    return false;
};

const resolveRepoRoot = () => {
    const candidates = [
        process.cwd(),
        path.resolve(__dirname, '../../../..'),
        path.resolve(__dirname, '../../../../..'),
    ];

    return candidates.find((candidate) => fs.existsSync(path.join(candidate, 'machine-core', 'go.mod'))) || null;
};

const buildLaunchConfig = (addr) => {
    const repoRoot = resolveRepoRoot();
    if (!repoRoot) {
        throw new Error('unable to locate repository root for machine-core autostart');
    }

    const machineCoreDir = path.join(repoRoot, 'machine-core');
    const dbPath = process.env.MACHINE_CORE_OTEL_DB_PATH
        || path.join(repoRoot, 'logs', 'machine-core-debug.sqlite');

    return {
        command: process.env.MACHINE_CORE_COMMAND || 'go',
        args: process.env.MACHINE_CORE_COMMAND
            ? process.env.MACHINE_CORE_COMMAND_ARGS?.split(/\s+/).filter(Boolean) || []
            : ['run', './cmd/machine-core', '--addr', addr],
        cwd: process.env.MACHINE_CORE_COMMAND ? repoRoot : machineCoreDir,
        env: {
            ...process.env,
            MACHINE_CORE_ADDR: addr,
            MACHINE_CORE_OTEL_DB_PATH: dbPath,
        },
        dbPath,
    };
};

const installShutdownHooks = () => {
    if (shutdownHooksInstalled) {
        return;
    }
    shutdownHooksInstalled = true;

    const stopChild = () => {
        if (child && !child.killed) {
            child.kill('SIGTERM');
        }
    };

    process.once('exit', stopChild);
    process.once('SIGINT', stopChild);
    process.once('SIGTERM', stopChild);
};

const shouldAutostart = () => {
    const transportEnabled = GO_MACHINE_CORE_MODE.test(
        String(process.env.MACHINE_CORE_TRANSPORT || process.env.MACHINE_CORE_MODE || '')
    );
    if (!transportEnabled) {
        return false;
    }

    const raw = `${process.env.MACHINE_CORE_AUTOSTART || 'true'}`.trim().toLowerCase();
    return !['0', 'false', 'no', 'off'].includes(raw);
};

const ensureMachineCoreProcess = async (addr = process.env.MACHINE_CORE_ADDR || DEFAULT_ADDR) => {
    if (!shouldAutostart()) {
        return false;
    }

    const normalizedAddr = normalizeAddress(addr);
    if (await canConnect(parseHostPort(normalizedAddr))) {
        log.info(`machine-core already reachable at ${normalizedAddr}`);
        return false;
    }

    if (startupPromise) {
        return startupPromise;
    }

    startupPromise = (async () => {
        const launch = buildLaunchConfig(normalizedAddr);
        log.info(`Starting machine-core process for ${normalizedAddr} with telemetry DB ${launch.dbPath}`);

        child = spawn(launch.command, launch.args, {
            cwd: launch.cwd,
            env: launch.env,
            stdio: ['ignore', 'pipe', 'pipe'],
        });

        child.stdout.on('data', (chunk) => {
            log.info(`[machine-core] ${`${chunk}`.trimEnd()}`);
        });
        child.stderr.on('data', (chunk) => {
            log.warn(`[machine-core] ${`${chunk}`.trimEnd()}`);
        });
        child.once('exit', (code, signal) => {
            log.warn(`machine-core process exited code=${code} signal=${signal || 'none'}`);
            child = null;
            startupPromise = null;
        });

        installShutdownHooks();

        const ready = await waitForReady(normalizedAddr);
        if (!ready) {
            throw new Error(`machine-core did not become ready at ${normalizedAddr}`);
        }

        log.info(`machine-core process is ready at ${normalizedAddr}`);
        return true;
    })();

    try {
        return await startupPromise;
    } catch (error) {
        startupPromise = null;
        if (child && !child.killed) {
            child.kill('SIGTERM');
        }
        child = null;
        throw error;
    }
};

export {
    ensureMachineCoreProcess,
    normalizeAddress,
    parseHostPort,
};
