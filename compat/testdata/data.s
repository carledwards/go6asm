; Data directives: numeric bases, char, word, asciiz, reserved fill.
    .byte 1, $FF, %1010, 'A'
    .word $1234, $0000
    .asciiz "HI"
    .res 4, $EE
