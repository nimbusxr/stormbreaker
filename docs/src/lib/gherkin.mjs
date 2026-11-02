/**
 * Step expressions in Gherkin code blocks: the parameter types (`{string}`,
 * `{element}`) stand apart from the text a step always has, and the brackets
 * of optional parts (`[[within {duration} ]]`) read as brackets. Injected
 * into Shiki's own Gherkin grammar, which leaves a step's text unscoped;
 * strings and comments keep their own colors.
 */
export const gherkinSteps = {
	name: 'sb-gherkin-steps',
	scopeName: 'text.gherkin.sb-steps',
	injectTo: ['text.gherkin.feature'],
	injectionSelector: 'L:text.gherkin.feature -string -comment',
	patterns: [
		{ match: '\\{(?:[A-Za-z][\\w-]*)?\\}', name: 'variable.parameter.sb' },
		{ match: '\\[\\[|\\]\\]', name: 'punctuation.definition.optional.sb' },
	],
};

/** A parameter type in a step expression, as the grammar above finds it. */
export const stepParam = /\{(?:[A-Za-z][\w-]*)?\}/g;
