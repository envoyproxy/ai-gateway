type BoxBorderStyle = {
    /**
     * Top left corner
     * @example `┌`
     * @example `╔`
     * @example `╓`
     */
    tl: string;
    /**
     * Top right corner
     * @example `┐`
     * @example `╗`
     * @example `╖`
     */
    tr: string;
    /**
     * Bottom left corner
     * @example `└`
     * @example `╚`
     * @example `╙`
     */
    bl: string;
    /**
     * Bottom right corner
     * @example `┘`
     * @example `╝`
     * @example `╜`
     */
    br: string;
    /**
     * Horizontal line
     * @example `─`
     * @example `═`
     * @example `─`
     */
    h: string;
    /**
     * Vertical line
     * @example `│`
     * @example `║`
     * @example `║`
     */
    v: string;
};
declare const boxStylePresets: Record<string, BoxBorderStyle>;
type BoxStyle = {
    /**
     * The border color
     * @default 'white'
     */
    borderColor: "black" | "red" | "green" | "yellow" | "blue" | "magenta" | "cyan" | "white" | "gray" | "blackBright" | "redBright" | "greenBright" | "yellowBright" | "blueBright" | "magentaBright" | "cyanBright" | "whiteBright";
    /**
     * The border style
     * @default 'solid'
     * @example 'single-double-rounded'
     * @example
     * ```ts
     * {
     *   tl: '┌',
     *   tr: '┐',
     *   bl: '└',
     *   br: '┘',
     *   h: '─',
     *   v: '│',
     * }
     * ```
     */
    borderStyle: BoxBorderStyle | keyof typeof boxStylePresets;
    /**
     * The vertical alignment of the text
     * @default 'center'
     */
    valign: "top" | "center" | "bottom";
    /**
     * The padding of the box
     * @default 2
     */
    padding: number;
    /**
     * The left margin of the box
     * @default 1
     */
    marginLeft: number;
    /**
     * The top margin of the box
     * @default 1
     */
    marginTop: number;
    /**
     * The top margin of the box
     * @default 1
     */
    marginBottom: number;
};
/**
 * The border options of the box
 */
type BoxOpts = {
    /**
     * Title that will be displayed on top of the box
     * @example 'Hello World'
     * @example 'Hello {name}'
     */
    title?: string;
    style?: Partial<BoxStyle>;
};
declare function box(text: string, _opts?: BoxOpts): string;

/**
 * Based on https://github.com/jorgebucaran/colorette
 * Read LICENSE file for more information
 * https://github.com/jorgebucaran/colorette/blob/20fc196d07d0f87c61e0256eadd7831c79b24108/index.js
 */
declare const colorDefs: {
    reset: (string: string) => string;
    bold: (string: string) => string;
    dim: (string: string) => string;
    italic: (string: string) => string;
    underline: (string: string) => string;
    inverse: (string: string) => string;
    hidden: (string: string) => string;
    strikethrough: (string: string) => string;
    black: (string: string) => string;
    red: (string: string) => string;
    green: (string: string) => string;
    yellow: (string: string) => string;
    blue: (string: string) => string;
    magenta: (string: string) => string;
    cyan: (string: string) => string;
    white: (string: string) => string;
    gray: (string: string) => string;
    bgBlack: (string: string) => string;
    bgRed: (string: string) => string;
    bgGreen: (string: string) => string;
    bgYellow: (string: string) => string;
    bgBlue: (string: string) => string;
    bgMagenta: (string: string) => string;
    bgCyan: (string: string) => string;
    bgWhite: (string: string) => string;
    blackBright: (string: string) => string;
    redBright: (string: string) => string;
    greenBright: (string: string) => string;
    yellowBright: (string: string) => string;
    blueBright: (string: string) => string;
    magentaBright: (string: string) => string;
    cyanBright: (string: string) => string;
    whiteBright: (string: string) => string;
    bgBlackBright: (string: string) => string;
    bgRedBright: (string: string) => string;
    bgGreenBright: (string: string) => string;
    bgYellowBright: (string: string) => string;
    bgBlueBright: (string: string) => string;
    bgMagentaBright: (string: string) => string;
    bgCyanBright: (string: string) => string;
    bgWhiteBright: (string: string) => string;
};
type ColorName = keyof typeof colorDefs;
type ColorFunction = (text: string | number) => string;
declare const colors: Record<"reset" | "bold" | "dim" | "italic" | "underline" | "inverse" | "hidden" | "strikethrough" | "black" | "red" | "green" | "yellow" | "blue" | "magenta" | "cyan" | "white" | "gray" | "bgBlack" | "bgRed" | "bgGreen" | "bgYellow" | "bgBlue" | "bgMagenta" | "bgCyan" | "bgWhite" | "blackBright" | "redBright" | "greenBright" | "yellowBright" | "blueBright" | "magentaBright" | "cyanBright" | "whiteBright" | "bgBlackBright" | "bgRedBright" | "bgGreenBright" | "bgYellowBright" | "bgBlueBright" | "bgMagentaBright" | "bgCyanBright" | "bgWhiteBright", ColorFunction>;
declare function getColor(color: ColorName, fallback?: ColorName): ColorFunction;
declare function colorize(color: ColorName, text: string | number): string;

declare function stripAnsi(text: string): string;
declare function centerAlign(str: string, len: number, space?: string): string;
declare function rightAlign(str: string, len: number, space?: string): string;
declare function leftAlign(str: string, len: number, space?: string): string;
declare function align(alignment: "left" | "right" | "center", str: string, len: number, space?: string): string;

export { BoxBorderStyle, BoxOpts, BoxStyle, ColorFunction, ColorName, align, box, centerAlign, colorize, colors, getColor, leftAlign, rightAlign, stripAnsi };
