/*
 * Copyright (C) 2021 Sienci Labs Inc.
 *
 * This file is part of gSender.
 *
 * gSender is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, under version 3 of the License.
 *
 * gSender is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with gSender.  If not, see <https://www.gnu.org/licenses/>.
 *
 * Contact for information regarding this program and its license
 * can be sent through gSender@sienci.com or mailed to the main office
 * of Sienci Labs Inc. in Waterloo, Ontario, Canada.
 *
 */

import ensureArray from 'ensure-array';
import noop from 'lodash/noop';
import { SerialPort } from 'serialport';
import socketIO from 'socket.io';
import { app } from 'electron';
import fs from 'fs';
import path from 'path';
import EventTrigger from '../../lib/EventTrigger';
import logger from '../../lib/logger';
import store from '../../store';
import config from '../configstore';
import taskRunner from '../taskrunner';
import { buildMachineCoreDeviceList, partitionDevicesForLegacySocket } from '../machine-core/devices';
import {
    shouldUseGoMachineCorePath,
    extractSessionID,
    relayMachineSessionSnapshot,
    relayMachineSessionEvent,
} from '../machine-core/sidecar';
import FlashingFirmware from '../../lib/Firmware/Flashing/firmwareflashing';
import {
    GrblController,
    GrblHalController
} from '../../controllers';
import { GRBL } from '../../controllers/Grbl/constants';
import { GRBLHAL } from '../../controllers/Grblhal/constants';
import { authorizeIPAddress } from '../../access-control';
import DFUFlasher from '../../lib/Firmware/Flashing/DFUFlasher';
import delay from '../../lib/delay';
import Connection from '../../lib/Connection';
import { VISUALIZER_SECONDARY } from '../../../app/src/constants';

const log = logger('service:cncengine');

// Case-insensitive equality checker.
// @param {string} str1 First string to check.
// @param {string} str2 Second string to check.
// @return {boolean} True if str1 and str2 are the same string, ignoring case.
const caseInsensitiveEquals = (str1, str2) => {
    str1 = str1 ? (str1 + '').toUpperCase() : '';
    str2 = str2 ? (str2 + '').toUpperCase() : '';
    return str1 === str2;
};

const isValidController = (controller) => (
    // Standard GRBL
    caseInsensitiveEquals(GRBL, controller) ||
    // GrblHal
    caseInsensitiveEquals(GRBLHAL, controller)
);

class CNCEngine {
    controllerClass = {};

    connection = null;

    listener = {
        taskStart: (...args) => {
            if (this.io) {
                this.io.emit('task:start', ...args);
            }
        },
        taskFinish: (...args) => {
            if (this.io) {
                this.io.emit('task:finish', ...args);
            }
        },
        taskError: (...args) => {
            if (this.io) {
                this.io.emit('task:error', ...args);
            }
        },
        configChange: (...args) => {
            if (this.io) {
                this.io.emit('config:change', ...args);
            }
        }
    };

    server = null;

    io = null;

    sockets = [];

    // File content and metadata
    gcode = null;

    meta = null;

    networkDevices = [];

    machineCoreSessions = new Map();

    machineCoreRequestCounter = 0;

    // Event Trigger
    event = new EventTrigger((event, trigger, commands) => {
        log.debug(`EventTrigger: event="${event}", trigger="${trigger}", commands="${commands}"`);
        if (trigger === 'system') {
            taskRunner.run(commands);
        }
    });

    async listDevices() {
        const serialPorts = await SerialPort.list();
        const configuredPorts = ensureArray(config.get('ports', []));
        const controllers = store.get('controllers', {});

        return buildMachineCoreDeviceList({
            serialPorts: serialPorts.concat(configuredPorts),
            controllers,
            networkDevices: this.networkDevices,
        });
    }

    async listSocketDevices() {
        if (!shouldUseGoMachineCorePath('device_list')) {
            return this.listDevices();
        }

        const startedAt = Date.now();
        try {
            // eslint-disable-next-line global-require
            const machineCore = require('../machine-core').default;
            const devices = await machineCore.listDevices();
            log.info(`machine-core handled device list via Go sidecar in ${Date.now() - startedAt}ms`);
            return devices;
        } catch (error) {
            log.warn(`machine-core sidecar listDevices failed after ${Date.now() - startedAt}ms: ${error.message}`);
            return this.listDevices();
        }
    }

    nextMachineCoreRequestID(prefix = 'machine-core') {
        this.machineCoreRequestCounter += 1;
        return `${prefix}-${Date.now()}-${this.machineCoreRequestCounter}`;
    }

    buildMachineCoreRPCContext({
        action,
        port,
        socketId = null,
        sessionId = null,
        commandType = null,
        controllerEvent = null,
        origin = 'node-cncengine',
    } = {}) {
        return {
            request_id: this.nextMachineCoreRequestID(action || 'machine-core'),
            action,
            port,
            socket_id: socketId || undefined,
            session_id: sessionId || undefined,
            command_type: commandType || undefined,
            controller_event: controllerEvent || undefined,
            origin,
        };
    }

    async syncMachineCoreSessionOpen(port, options = {}, socket) {
        if (!shouldUseGoMachineCorePath('session_open')) {
            return null;
        }

        const existingSessionId = this.machineCoreSessions.get(port);
        if (existingSessionId) {
            return existingSessionId;
        }

        const startedAt = Date.now();
        // eslint-disable-next-line global-require
        const machineCore = require('../machine-core').default;
        const result = await machineCore.openSession({
            device_id: port,
            baud_rate: options.baudrate,
            rtscts: options.rtscts,
            network_port: options.ethernetPort || options.networkPort,
            default_firmware: options.defaultFirmware,
            client_id: socket?.id,
        }, this.buildMachineCoreRPCContext({
            action: 'session_open',
            port,
            socketId: socket?.id,
        }));
        const sessionId = extractSessionID(result);
        if (!sessionId) {
            throw new Error(`machine-core openSession returned no session for ${port}`);
        }
        this.machineCoreSessions.set(port, sessionId);
        log.info(`machine-core handled session open for ${port} via Go sidecar in ${Date.now() - startedAt}ms`);
        return sessionId;
    }

    async syncMachineCoreSessionAttach(port, socket) {
        if (!shouldUseGoMachineCorePath('session_attach')) {
            return false;
        }

        const startedAt = Date.now();
        try {
            // eslint-disable-next-line global-require
            const machineCore = require('../machine-core').default;
            const snapshot = await machineCore.resolveSession(port, socket.id, this.buildMachineCoreRPCContext({
                action: 'session_attach',
                port,
                socketId: socket?.id,
            }));
            const sessionId = snapshot?.session?.id;
            if (!sessionId) {
                return false;
            }
            this.machineCoreSessions.set(port, sessionId);
            let replayCount = 0;
            relayMachineSessionSnapshot(socket, snapshot, this);
            await machineCore.replayEvents(sessionId, socket.id, (event) => {
                replayCount += 1;
                relayMachineSessionEvent(socket, event, this);
            }, this.buildMachineCoreRPCContext({
                action: 'session_replay',
                port,
                socketId: socket?.id,
                sessionId,
            }));
            log.info(`machine-core handled session attach for ${port} via Go sidecar in ${Date.now() - startedAt}ms with ${replayCount} replay events`);
            return true;
        } catch (error) {
            log.warn(`machine-core sidecar attach failed for ${port} after ${Date.now() - startedAt}ms: ${error.message}`);
            return false;
        }
    }

    async syncMachineCoreSessionClose(port, socketId = null) {
        if (!shouldUseGoMachineCorePath('session_close')) {
            return false;
        }

        const sessionId = this.machineCoreSessions.get(port);
        if (!sessionId) {
            return false;
        }

        this.machineCoreSessions.delete(port);

        const startedAt = Date.now();
        try {
            // eslint-disable-next-line global-require
            const machineCore = require('../machine-core').default;
            await machineCore.closeSession(sessionId, this.buildMachineCoreRPCContext({
                action: 'session_close',
                port,
                socketId,
                sessionId,
            }));
            log.info(`machine-core handled session close for ${port} via Go sidecar in ${Date.now() - startedAt}ms`);
            return true;
        } catch (error) {
            log.warn(`machine-core sidecar closeSession failed for ${port} after ${Date.now() - startedAt}ms: ${error.message}`);
            return false;
        }
    }

    async syncMachineCoreFileLoad(port, gcode, meta = {}, rpcContext = null) {
        if (!shouldUseGoMachineCorePath('file_shadow') || !port || !gcode) {
            return false;
        }

        const sessionId = this.machineCoreSessions.get(port);
        if (!sessionId) {
            return false;
        }

        const startedAt = Date.now();
        // eslint-disable-next-line global-require
        const machineCore = require('../machine-core').default;
        await machineCore.loadFile(sessionId, {
            name: meta.name,
            gcode,
            metadata: {
                size: meta.size,
                visualizer: meta.visualizer,
                port,
            },
        }, rpcContext || this.buildMachineCoreRPCContext({
            action: 'file_load',
            port,
            sessionId,
        }));
        log.info(`machine-core handled file load for ${port} via Go sidecar in ${Date.now() - startedAt}ms`);
        return true;
    }

    async syncMachineCoreFileUnload(port, rpcContext = null) {
        if (!shouldUseGoMachineCorePath('file_shadow') || !port) {
            return false;
        }

        const sessionId = this.machineCoreSessions.get(port);
        if (!sessionId) {
            return false;
        }

        const startedAt = Date.now();
        // eslint-disable-next-line global-require
        const machineCore = require('../machine-core').default;
        await machineCore.unloadFile(sessionId, rpcContext || this.buildMachineCoreRPCContext({
            action: 'file_unload',
            port,
            sessionId,
        }));
        log.info(`machine-core handled file unload for ${port} via Go sidecar in ${Date.now() - startedAt}ms`);
        return true;
    }

    async syncMachineCoreJobCommand(port, cmd, args = [], rpcContext = null) {
        if (!shouldUseGoMachineCorePath('job_shadow') || !port) {
            return false;
        }

        const sessionId = this.machineCoreSessions.get(port);
        if (!sessionId) {
            return false;
        }

        const startedAt = Date.now();
        // eslint-disable-next-line global-require
        const machineCore = require('../machine-core').default;
        switch (cmd) {
        case 'feeder:start':
            await machineCore.startJob(sessionId, rpcContext || this.buildMachineCoreRPCContext({
                action: 'job_start',
                port,
                sessionId,
                commandType: cmd,
            }));
            log.info(`machine-core handled startJob for ${port} via Go sidecar in ${Date.now() - startedAt}ms`);
            return true;
        case 'pause':
        case 'gcode:pause':
            await machineCore.pauseJob(sessionId, rpcContext || this.buildMachineCoreRPCContext({
                action: 'job_pause',
                port,
                sessionId,
                commandType: cmd,
            }));
            log.info(`machine-core handled pauseJob for ${port} via Go sidecar in ${Date.now() - startedAt}ms`);
            return true;
        case 'resume':
        case 'gcode:resume':
            await machineCore.resumeJob(sessionId, rpcContext || this.buildMachineCoreRPCContext({
                action: 'job_resume',
                port,
                sessionId,
                commandType: cmd,
            }));
            log.info(`machine-core handled resumeJob for ${port} via Go sidecar in ${Date.now() - startedAt}ms`);
            return true;
        case 'stop':
        case 'gcode:stop':
        case 'feeder:stop':
            await machineCore.stopJob(sessionId, args[0] || {}, rpcContext || this.buildMachineCoreRPCContext({
                action: 'job_stop',
                port,
                sessionId,
                commandType: cmd,
            }));
            log.info(`machine-core handled stopJob for ${port} via Go sidecar in ${Date.now() - startedAt}ms`);
            return true;
        default:
            return false;
        }
    }

    async dispatchControllerCommand(port, cmd, args = [], controller = null, socketId = null) {
        const activeController = controller || store.get(`controllers["${port}"]`);
        if (!activeController) {
            throw new Error(`controller on "${port}" not accessible`);
        }

        const sessionId = this.machineCoreSessions.get(port);
        const machineCoreHandled = await this.syncMachineCoreJobCommand(port, cmd, args, this.buildMachineCoreRPCContext({
            action: 'controller_command',
            port,
            socketId,
            sessionId,
            commandType: cmd,
        }));
        activeController.command.apply(activeController, [cmd].concat(args));
        return machineCoreHandled;
    }

    async loadWithMachineCore({ port, gcode, socketId = null, ...meta }) {
        const useMachineCoreAuthority = shouldUseGoMachineCorePath('file_shadow')
            && Boolean(port)
            && Boolean(this.machineCoreSessions.get(port));

        if (useMachineCoreAuthority) {
            await this.syncMachineCoreFileLoad(port, gcode, meta, this.buildMachineCoreRPCContext({
                action: 'file_load',
                port,
                socketId,
                sessionId: this.machineCoreSessions.get(port),
            }));
        }

        this.gcode = gcode;
        this.meta = meta;

        if (port) {
            const controller = store.get(`controllers["${port}"]`);
            if (controller) {
                controller.loadFile(this.gcode, this.meta);
            }
        }

        log.info(`Loaded file '${meta.name}' to CNCEngine`);
        this.emit('file:load', gcode, meta.size, meta.name, meta.visualizer);
    }

    async unloadWithMachineCore(port = this.connection?.options?.port, socketId = null) {
        const useMachineCoreAuthority = shouldUseGoMachineCorePath('file_shadow')
            && Boolean(port)
            && Boolean(this.machineCoreSessions.get(port));

        if (useMachineCoreAuthority) {
            await this.syncMachineCoreFileUnload(port, this.buildMachineCoreRPCContext({
                action: 'file_unload',
                port,
                socketId,
                sessionId: this.machineCoreSessions.get(port),
            }));
        }

        log.info('Unloading file from CNCEngine');
        this.gcode = null;
        this.meta = null;
        this.emit('file:unload');
    }

    runMachineCoreSidecar(task) {
        Promise.resolve(task).catch((error) => {
            log.warn(`machine-core sidecar task failed: ${error.message}`);
        });
    }

    stringifyMachineCoreTelemetryValue(value) {
        if (value === undefined || value === null) {
            return undefined;
        }
        if (typeof value === 'string') {
            return value;
        }
        if (typeof value === 'number' || typeof value === 'boolean') {
            return String(value);
        }
        return JSON.stringify(value);
    }

    buildMachineCoreTelemetryMetadata(eventName, args = []) {
        switch (eventName) {
        case 'controller:state': {
            const [controllerType, controllerState] = args;
            return {
                controller_type: this.stringifyMachineCoreTelemetryValue(controllerType),
                controller_state: this.stringifyMachineCoreTelemetryValue(controllerState),
            };
        }
        case 'controller:settings': {
            const [controllerType, controllerSettings] = args;
            return {
                controller_type: this.stringifyMachineCoreTelemetryValue(controllerType),
                controller_settings: this.stringifyMachineCoreTelemetryValue(controllerSettings),
            };
        }
        case 'sender:status':
            return {
                sender_status: this.stringifyMachineCoreTelemetryValue(args[0]),
            };
        case 'feeder:status':
            return {
                feeder_status: this.stringifyMachineCoreTelemetryValue(args[0]),
            };
        case 'workflow:state':
            return {
                workflow_state: this.stringifyMachineCoreTelemetryValue(args[0]),
            };
        case 'homing:has-homed':
            return {
                homing_has_homed: this.stringifyMachineCoreTelemetryValue(Boolean(args[0])),
            };
        case 'error': {
            const [errorPayload] = args;
            const errorType = `${errorPayload?.type || ''}`.trim().toLowerCase();
            return {
                error_code: this.stringifyMachineCoreTelemetryValue(errorPayload?.code),
                error_message: this.stringifyMachineCoreTelemetryValue(errorPayload?.description || errorPayload?.message),
                error_is_alarm: this.stringifyMachineCoreTelemetryValue(errorType === 'alarm'),
                has_alarm: this.stringifyMachineCoreTelemetryValue(errorType === 'alarm'),
            };
        }
        default:
            return null;
        }
    }

    async syncMachineCoreRuntimeTelemetry(port, eventName, args = []) {
        const sessionId = this.machineCoreSessions.get(port);
        if (!sessionId) {
            return false;
        }

        const metadata = this.buildMachineCoreTelemetryMetadata(eventName, args);
        if (!metadata) {
            return false;
        }

        const compactMetadata = Object.entries(metadata).reduce((accumulator, [key, value]) => {
            if (value !== undefined && value !== null && value !== '') {
                accumulator[key] = value;
            }
            return accumulator;
        }, {});
        if (Object.keys(compactMetadata).length === 0) {
            return false;
        }

        // eslint-disable-next-line global-require
        const machineCore = require('../machine-core').default;
        await machineCore.sendCommand(sessionId, {
            type: 'status_report',
            metadata: compactMetadata,
        }, this.buildMachineCoreRPCContext({
            action: 'runtime_telemetry',
            port,
            sessionId,
            commandType: 'status_report',
            controllerEvent: eventName,
        }));
        return true;
    }

    installMachineCoreTelemetryBridge(controller, port) {
        if (!controller || typeof controller.emit !== 'function' || !port) {
            return controller;
        }
        if (controller.__machineCoreTelemetryBridgeInstalled) {
            return controller;
        }

        const originalEmit = controller.emit.bind(controller);
        const telemetryEvents = new Set([
            'controller:state',
            'controller:settings',
            'sender:status',
            'feeder:status',
            'workflow:state',
            'homing:has-homed',
            'error',
        ]);

        controller.emit = (eventName, ...args) => {
            const result = originalEmit(eventName, ...args);
            if (telemetryEvents.has(eventName)) {
                this.runMachineCoreSidecar(this.syncMachineCoreRuntimeTelemetry(port, eventName, args));
            }
            return result;
        };
        controller.__machineCoreTelemetryBridgeInstalled = true;
        return controller;
    }

    // @param {object} server The HTTP server instance.
    // @param {string} controller Specify CNC controller.
    start(server, controller = '') {
        // Fallback to an empty string if the controller is not valid
        log.debug(controller);
        if (!isValidController(controller)) {
            controller = '';
        }

        // Grbl
        if (!controller || caseInsensitiveEquals(GRBL, controller)) {
            this.controllerClass[GRBL] = GrblController;
        }
        if (!controller || caseInsensitiveEquals(GRBLHAL, controller)) {
            this.controllerClass[GRBLHAL] = GrblHalController;
        }

        if (Object.keys(this.controllerClass).length === 0) {
            throw new Error(`No valid CNC controller specified (${controller})`);
        }

        const loadedControllers = Object.keys(this.controllerClass);
        log.debug(`Loaded controllers: ${loadedControllers}`);

        this.stop();

        taskRunner.on('start', this.listener.taskStart);
        taskRunner.on('finish', this.listener.taskFinish);
        taskRunner.on('error', this.listener.taskError);
        config.on('change', this.listener.configChange);

        // System Trigger: Startup
        this.event.trigger('startup');

        this.server = server;
        this.io = socketIO(this.server, {
            serveClient: true,
            path: '/socket.io',
            pingTimeout: 60000,
            pingInterval: 25000,
            maxHttpBufferSize: 40e6
        });

        this.io.use(async (socket, next) => {
            try {
                // IP Address Access Control
                const ipaddr = socket.handshake.address;
                await authorizeIPAddress(ipaddr);
            } catch (err) {
                log.warn(err);
                next(err);
                return;
            }

            next();
        });

        this.io.on('connection', (socket) => {
            this.networkDevices = [];
            const address = socket.handshake.address;
            const user = socket.decoded_token || {};
            log.debug(`New connection from ${address}: id=${socket.id}, user.id=${user.id}, user.name=${user.name}`);

            const connectionListeners = {
                'serialport:open': (port, baudrate, controllerType, inuse) => {
                    this.emit('serialport:open', port, baudrate, controllerType, inuse);
                },
                'serialport:close': (options, received) => {
                    this.connection = null;
                    this.emit('serialport:close', options, received);
                },
                'firmwareFound': (controllerType = GRBL, options, callback = noop, refresh = false) => {
                    let { port, baudrate, rtscts, network } = { ...options };
                    log.debug('firmwareFound event fired');
                    if (typeof callback !== 'function') {
                        callback = noop;
                    }

                    let controller = store.get(`controllers["${port}"]`);
                    if (!controller) {
                        log.debug('making new controller');
                        const Controller = this.controllerClass[controllerType];
                        if (!Controller) {
                            const err = `Not supported controller: ${controllerType}`;
                            log.error(err);
                            callback(new Error(err));
                            return;
                        }

                        controller = new Controller(this, this.connection, {
                            port: port,
                            baudrate: baudrate,
                            rtscts: !!rtscts,
                            network
                        });
                    }

                    this.installMachineCoreTelemetryBridge(controller, port);

                    controller.addConnection(socket);


                    // Load file to controller if it exists
                    if (this.hasFileLoaded()) {
                        controller.loadFile(this.gcode, this.meta, refresh);
                        socket.emit('file:load', this.gcode, this.meta.size, this.meta.name);
                    } else {
                        log.debug('No file in CNCEngine to load to sender');
                    }

                    this.connection.addController(controller);

                    controller.open(port, baudrate, refresh, (err = null) => {
                        if (err) {
                            callback(err);
                            return;
                        }

                        // Throw error if port is used and it's not a second client connecting
                        if (!refresh && store.get(`controllers["${port}"]`)) {
                            log.error(`Serial port "${port}" was not properly closed`);
                        }
                        store.set(`controllers[${JSON.stringify(port)}]`, controller);

                        callback(null);
                    });

                    socket.emit('serialport:openController', controllerType);
                }
            };

            const addConnectionListeners = () => {
                Object.keys(connectionListeners).forEach((eventName) => {
                    const callback = connectionListeners[eventName];
                    this.connection.on(eventName, callback);
                });
            };

            const removeConnectionListeners = () => {
                Object.keys(connectionListeners).forEach((eventName) => {
                    this.connection.removeAllListeners(eventName);
                });
            };

            // Add to the socket pool
            this.sockets.push(socket);

            socket.emit('startup', {
                loadedControllers: Object.keys(this.controllerClass),

                // User-defined baud rates and ports
                baudrates: ensureArray(config.get('baudrates', [])),
                ports: ensureArray(config.get('ports', [])),
                socketsLength: this.sockets.length
            });

            socket.on('newConnection', () => {
                // if the sockets include more than the original desktop client
                // check if electron app is defined
                if (this.sockets.length > 1 && app) {
                    const userDataPath = path.join(app.getPath('userData'), 'preferences.json');

                    if (fs.existsSync(userDataPath)) {
                        const content = fs.readFileSync(userDataPath, 'utf8') || '{}';
                        socket.emit('connection:new', content);
                    }
                }
            });

            socket.on('disconnect', () => {
                log.debug(`Disconnected from ${address}: id=${socket.id}, user.id=${user.id}, user.name=${user.name}`);

                if (!this.connection) {
                    return;
                }
                this.connection.removeConnection(socket);

                // Remove from socket pool
                this.sockets.splice(this.sockets.indexOf(socket), 1);
            });

            socket.on('reconnect', (port) => {
                this.runMachineCoreSidecar(this.syncMachineCoreSessionAttach(port, socket));

                if (!this.connection) {
                    const message = 'No connection object found to reconnect to';
                    log.info(message);
                    this.io.emit('task:error', message);
                    return;
                }
                log.info(`Reconnecting to open controller on port ${port} with socket ID ${socket.id}`);
                this.connection.addConnection(socket);
                if (this.connection.isOpen()) {
                    log.info('Joining port room on socket');
                    socket.join(port);
                } else {
                    log.info('connection no longer open');
                }

                let controller = store.get(`controllers["${port}"]`);
                if (!controller) {
                    const message = `No controller found on port ${port} to reconnect to`;
                    log.info(message);
                    this.io.emit('task:error', message);
                    return;
                }
                log.info(`Reconnecting to open controller on port ${port} with socket ID ${socket.id}`);
                controller.addConnection(socket);
                log.info(`Controller state: ${controller.isOpen()}`);
                if (this.connection.isOpen()) {
                    log.info('Joining port room on socket');
                    socket.join(port);
                } else {
                    log.info('Connection no longer open');
                }
            });

            socket.on('addclient', (port) => {
                this.runMachineCoreSidecar(this.syncMachineCoreSessionAttach(port, socket));

                if (!this.connection) {
                    log.info('No connection object found to reconnect to');
                    return;
                }
                log.info(`Adding new client to connection on port ${port} with socket ID ${socket.id}`);
                this.connection.addConnection(socket);
                log.info(`connection state: ${this.connection.isOpen()}`);

                let controller = store.get(`controllers["${port}"]`);
                if (!controller) {
                    log.info(`No controller found on port ${port} to reconnect to`);
                    return;
                }
                log.info(`Adding new client to controller on port ${port} with socket ID ${socket.id}`);
                controller.addConnection(socket);
                log.info(`Connection state: ${this.connection.isOpen()}`);
            });

            // List the available serial ports
            socket.on('list', async () => {
                log.debug(`socket.list(): id=${socket.id}`);

                try {
                    const devices = await this.listSocketDevices();
                    const { recognizedPorts, unrecognizedPorts, networkPorts } = partitionDevicesForLegacySocket(devices);
                    socket.emit('serialport:list', recognizedPorts, unrecognizedPorts, networkPorts);
                } catch (err) {
                    log.error(err);
                }
            });

            //Sends back a list of available IPs in the computer
            socket.on('listAllIps', () => {
                const { networkInterfaces } = require('os');
                const _networkInterfaces = networkInterfaces();
                const ipList = [];

                //Create a list of network list name: [{IP1},{IP2}...]
                for (const networkName of Object.keys(_networkInterfaces)) {
                    for (const ips of _networkInterfaces[networkName]) {
                        //Consider only IPV4 addresses
                        if (ips.family === 'IPv4') {
                            if (ipList.indexOf(ips.address) < 0) {
                                ipList.push(ips.address);
                            }
                        }
                    }
                }
                socket.emit('ip:list', ipList);
            });

            // Open serial port
            socket.on('open', async (port, options, callback) => {
                const engine = this;
                let reservedGoSession = false;

                log.debug(`socket.open("${port}", ${JSON.stringify(options)}): id=${socket.id}`);

                // Remove old listeners from the existing connection before potentially replacing it
                if (this.connection) {
                    removeConnectionListeners();
                }

                if (!this.connection || this.connection.isClose()) {
                    // No connection or stale closed connection — start fresh
                    this.connection = new Connection(engine, port, options, callback);
                    addConnectionListeners();
                } else {
                    // Genuinely open connection — refresh for additional client joining
                    addConnectionListeners();
                    this.connection.updateOptions(options);
                    this.connection.refresh();
                }

                this.connection.addConnection(socket);

                if (this.connection.isOpen()) {
                    // Join the room
                    socket.join(port);

                    callback(null);
                    return;
                }

                if (shouldUseGoMachineCorePath('session_open')) {
                    try {
                        await this.syncMachineCoreSessionOpen(port, options, socket);
                        reservedGoSession = true;
                    } catch (error) {
                        log.warn(`machine-core sidecar openSession failed for ${port}: ${error.message}`);
                        this.connection = null;
                        callback(error);
                        return;
                    }
                }

                this.connection.open((err = null) => {
                    if (err) {
                        if (reservedGoSession) {
                            this.runMachineCoreSidecar(this.syncMachineCoreSessionClose(port, socket?.id));
                        }
                        callback(err);
                        this.connection = null;
                        return;
                    }

                    // System Trigger: Open a serial port
                    this.event.trigger('port:open');

                    callback(null);
                });
            });

            // Close serial port
            socket.on('close', (port, callback = noop) => {
                const numClients = socket.adapter.rooms?.get(port)?.size;
                if (typeof callback !== 'function') {
                    callback = noop;
                }

                log.debug(`socket.close("${port}"): id=${socket.id}`);

                const controller = store.get(`controllers["${port}"]`);
                if (!controller) {
                    const err = `Controller on "${port}" not accessible`;
                    log.error(err);
                    callback(new Error(err));
                    return;
                }

                if (!this.connection) {
                    const err = `Serial port "${port}" not accessible`;
                    log.error(err);
                    callback(new Error(err));
                    return;
                }

                // System Trigger: Close a serial port
                this.event.trigger('port:close');

                // Leave the room
                socket.leave(port);

                if (!numClients || numClients <= 1) { // if only this one was connected
                    this.connection.close();
                    this.connection = null;
                    this.runMachineCoreSidecar(this.syncMachineCoreSessionClose(port, socket?.id));
                    controller.close(err => {
                        // Remove controller from store
                        store.unset(`controllers[${JSON.stringify(port)}]`);

                        // Destroy controller
                        controller.destroy();

                        callback(null);
                    });
                }

                socket.emit('serialport:close', {
                    port: port,
                });
            });

            socket.on('command', async (port, cmd, ...args) => {
                log.debug(`socket.command("${port}", "${cmd}"): id=${socket.id}`);

                if (!this.connection || this.connection.isClose()) {
                    log.error(`Serial port "${port}" not accessible`);
                    return;
                }

                try {
                    await this.dispatchControllerCommand(port, cmd, args, null, socket.id);
                } catch (error) {
                    log.error(error.message);
                    socket.emit('task:error', error.message);
                }
            });

            socket.on('flash:start', (flashPort, imageType, isHal = false, data = null) => {
                log.debug(`Flashing ${flashPort}, isHal: ${isHal}, imageType: ${imageType}`);
                if (!flashPort) {
                    log.error('task:error', 'No port specified - make sure you connect to you device at least once before attempting flashing');
                    return;
                }
                let halFlasher;
                if (isHal) {
                    halFlasher = new DFUFlasher({
                        image: imageType,
                        isHal,
                        hex: data
                    });

                    halFlasher.on('error', (err) => {
                        this.emit('flash:message', { type: 'Error', content: err });
                    });

                    halFlasher.on('info', (msg) => {
                        this.emit('flash:message', { type: 'Info', content: msg });
                    });

                    halFlasher.on('end', () => {
                        this.emit('flash:end');
                    });
                    halFlasher.on('progress', (amount, total) => {
                        this.emit('flash:progress', amount, total);
                    });
                }

                const isInDFUmode = flashPort === 'SLB_DFU';

                //Close the controller for flasher utility to take over the port
                const controller = store.get('controllers["' + flashPort + '"]');
                if (controller) {
                    if (isHal) {
                        store.unset(`controllers[${JSON.stringify(flashPort)}]`);
                        const startFlash = () => {
                            try {
                                halFlasher.flash(data);
                            } catch (err) {
                                this.emit('flash:message', { type: 'Error', content: err });
                            }
                        };
                        if (isInDFUmode) {
                            startFlash();
                        } else {
                            delay(1500).then(startFlash);
                        }
                        return;
                    }

                    // Normal flash - close port then flash using AVRgirl
                    this.connection.close();
                    controller.close(
                        () => {
                            FlashingFirmware(flashPort, imageType, socket);
                        }
                    );

                    store.unset(`controllers[${JSON.stringify(flashPort)}]`);

                    return;
                } else if (isHal) {
                    const startFlash = () => {
                        try {
                            halFlasher.flash(data);
                        } catch (err) {
                            this.emit('flash:message', { type: 'Error', content: err });
                        }
                    };
                    if (isInDFUmode) {
                        startFlash();
                    } else {
                        delay(1500).then(startFlash);
                    }

                    return;
                }

                FlashingFirmware(flashPort, imageType, socket);
            });

            socket.on('write', (port, data, context = {}) => {
                log.debug(`socket.write("${port}", "${data}", ${JSON.stringify(context)}): id=${socket.id}`);

                const controller = store.get(`controllers["${port}"]`);
                if (!this.connection || this.connection.isClose() || !controller || controller.isClose()) {
                    log.error(`Serial port "${port}" not accessible`);
                    return;
                }

                controller.write(data, context);
            });

            socket.on('writeln', (port, data, context = {}) => {
                log.debug(`socket.writeln("${port}", "${data}", ${JSON.stringify(context)}): id=${socket.id}`);
                store.set('inAppConsoleInput', data);
                const controller = store.get(`controllers["${port}"]`);
                if (!this.connection || this.connection.isClose() || !controller || controller.isClose()) {
                    log.error(`Serial port "${port}" not accessible`);
                    return;
                }

                controller.writeln(data, context);
            });

            socket.on('hPing', () => {
                log.debug(`Health check received at ${new Date().toLocaleTimeString()}`);
                socket.emit('hPong');
            });

            socket.on('file:fetch', () => {
                socket.emit('file:fetch', this.gcode, this.meta);
            });

            socket.on('file:unload', () => {
                log.debug('Socket unload called');
                this.unload();
            });
        });
    }

    stop() {
        if (this.io) {
            this.io.close();
            this.io = null;
        }
        this.sockets = [];
        this.server = null;

        taskRunner.removeListener('start', this.listener.taskStart);
        taskRunner.removeListener('finish', this.listener.taskFinish);
        taskRunner.removeListener('error', this.listener.taskError);
        config.removeListener('change', this.listener.configChange);
    }

    // Emit message across all sockets
    emit(msg, ...args) {
        this.sockets.forEach((socket) => {
            socket.emit(msg, ...args);
        });
    }

    /* Functions related to loading file through server */
    // If gcode is going to live in CNCengine, we need functions to access or unload it.
    load({ port, gcode, ...meta }) {
        this.runMachineCoreSidecar(this.loadWithMachineCore({ port, gcode, ...meta }));
    }

    unload() {
        this.runMachineCoreSidecar(this.unloadWithMachineCore());
    }

    fetchGcode() {
        return [this.gcode, this.meta];
    }

    hasFileLoaded() {
        // this function is for checking whether we need to reload a file to the main vis,
        // so if the file we loaded was in secondary vis, return false
        if (this.meta?.visualizer === VISUALIZER_SECONDARY) {
            return false;
        }
        return this.gcode !== null;
    }
}

export default CNCEngine;
