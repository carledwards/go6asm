; Macros with parameters, .paramcount, and a macro body that uses
; .repeat — all must expand byte-identically to ca65.
.macro STORE val, addr
    LDA #val
    STA addr
.endmacro

.macro NOPS n
    .repeat n
        NOP
    .endrepeat
.endmacro

    STORE $11, $0200
    STORE $22, $0201
    NOPS 3
    RTS
