; .repeat unrolling with and without a loop variable.
.repeat 4, i
    .byte i
.endrepeat
.repeat 2
    NOP
.endrepeat
    RTS
