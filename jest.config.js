module.exports = {
    testEnvironment: 'jsdom',
    setupFilesAfterEnv: ['<rootDir>/jest.setup.js'],
    automock: false,
    transform: {
        '^.+\\.[jt]sx?$': ['babel-jest', { configFile: './babel.config.js' }],
    },
    transformIgnorePatterns: [
        '/node_modules/(?!(three)/)',
    ],
    testPathIgnorePatterns: [
        '/node_modules/',
        'App.test.tsx',
        '<rootDir>/test/evaluate-assignment-expression.js',
        '<rootDir>/test/evaluate-expression.js',
        '<rootDir>/test/ensure-type.js',
        '<rootDir>/test/grbl.js',
        '<rootDir>/test/machine-core-adapter.test.js',
        '<rootDir>/test/sender.js',
        '<rootDir>/test/translate-expression.js',
    ],
    modulePathIgnorePatterns: [
        '<rootDir>/dist/',
        '<rootDir>/src/package.json',
        '<rootDir>/src/app/package.json',
    ],
    moduleNameMapper: {
        '\\.(css|less|scss|sass|styl)$': '<rootDir>/__mocks__/styleMock.js',
        '\\.(jpg|jpeg|png|gif|svg)$': '<rootDir>/__mocks__/fileMock.js',
        '^app/(.*)$': '<rootDir>/src/app/src/$1',
        '^(\\.{1,2}/)*config/settings$': '<rootDir>/src/app/src/config/__mocks__/settings.ts',
    },
    testMatch: ['**/__tests__/**/*.[jt]s?(x)', '**/*.test.[jt]s?(x)'],
    haste: {
        forceNodeFilesystemAPI: true,
    },
};
